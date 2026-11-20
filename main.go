package main

// rescue-sshd для Android/Magisk: минимальный SSH-сервер без зависимостей от Termux.
// Ключи и лог лежат в /data/local/rescue. Слушает только private-LAN IP (по умолчанию)
// и только по публичному ключу. Запускается root-ом через Magisk service.d.

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/ssh"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"flag"
	"fmt"
	"log"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
)

const (
	defDir      = "/data/local/rescue"
	defPort     = 2222
	defShell    = "/system/bin/sh"
)

func loadOrCreateHostKey(path string, logger *log.Logger) (ssh.Signer, error) {
	if b, err := os.ReadFile(path); err == nil {
		return ssh.ParsePrivateKey(b)
	} else if !os.IsNotExist(err) {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return nil, err
	}
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, err
	}
	der, err := x509.MarshalPKCS8PrivateKey(priv)
	if err != nil {
		return nil, err
	}
	b := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})
	if err := os.WriteFile(path, b, 0600); err != nil {
		return nil, err
	}
	logger.Printf("создан host key: %s", path)
	return ssh.NewSignerFromKey(priv)
}

func loadAuthorized(path string) ([]ssh.PublicKey, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var out []ssh.PublicKey
	for _, line := range bytes.Split(data, []byte("\n")) {
		line = bytes.TrimSpace(line)
		if len(line) == 0 || bytes.HasPrefix(line, []byte("#")) {
			continue
		}
		k, _, _, _, err := ssh.ParseAuthorizedKey(line)
		if err != nil {
			continue
		}
		out = append(out, k)
	}
	return out, nil
}

func privateIPv4() string {
	ifs, err := net.Interfaces()
	if err != nil {
		return "127.0.0.1"
	}
	for _, ifi := range ifs {
		if ifi.Flags&net.FlagUp == 0 || ifi.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, err := ifi.Addrs()
		if err != nil {
			continue
		}
		for _, a := range addrs {
			var ip net.IP
			switch v := a.(type) {
			case *net.IPNet:
				ip = v.IP
			case *net.IPAddr:
				ip = v.IP
			}
			if ip == nil {
				continue
			}
			if ip4 := ip.To4(); ip4 != nil && ip4.IsPrivate() {
				return ip4.String()
			}
		}
	}
	return "127.0.0.1"
}

func main() {
	dir := flag.String("dir", defDir, "каталог с ключами (вне Termux)")
	listen := flag.String("listen", "", "адрес:порт (по умолчанию: приватный LAN IP:2222)")
	keysPath := flag.String("keys", "", "authorized_keys (по умолчанию <dir>/authorized_keys)")
	hostKey := flag.String("hostkey", "", "host private key (по умолчанию <dir>/host_ed25519)")
	shellPath := flag.String("shell", defShell, "shell для сессий")
	flag.Parse()

	if err := os.MkdirAll(*dir, 0700); err != nil {
		fmt.Fprintln(os.Stderr, "mkdir:", err)
		os.Exit(1)
	}
	logPath := filepath.Join(*dir, "rescue.log")
	lf, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		fmt.Fprintln(os.Stderr, "log:", err)
		os.Exit(1)
	}
	defer lf.Close()
	logger := log.New(lf, "rescue ", log.LstdFlags)

	kp := *keysPath
	if kp == "" {
		kp = filepath.Join(*dir, "authorized_keys")
	}
	hp := *hostKey
	if hp == "" {
		hp = filepath.Join(*dir, "host_ed25519")
	}
	authorized, err := loadAuthorized(kp)
	if err != nil {
		logger.Fatalf("authorized_keys: %v", err)
	}
	if len(authorized) == 0 {
		logger.Fatalf("authorized_keys пуст — вход по ключу невозможен")
	}
	signer, err := loadOrCreateHostKey(hp, logger)
	if err != nil {
		logger.Fatalf("host key: %v", err)
	}

	cfg := &ssh.ServerConfig{
		MaxAuthTries: 3,
		PublicKeyCallback: func(meta ssh.ConnMetadata, key ssh.PublicKey) (*ssh.Permissions, error) {
			for _, k := range authorized {
				if bytes.Equal(k.Marshal(), key.Marshal()) {
					return &ssh.Permissions{}, nil
				}
			}
			return nil, errors.New("key not authorized")
		},
	}
	cfg.AddHostKey(signer)

	laddr := *listen
	if laddr == "" {
		laddr = net.JoinHostPort(privateIPv4(), strconv.Itoa(defPort))
	}
	ln, err := net.Listen("tcp", laddr)
	if err != nil {
		logger.Fatalf("listen %s: %v", laddr, err)
	}
	logger.Printf("ready on %s (shell=%s)", laddr, *shellPath)

	for {
		c, err := ln.Accept()
		if err != nil {
			logger.Printf("accept: %v", err)
			continue
		}
		go serveConn(c, cfg, *shellPath, logger)
	}
}

func serveConn(c net.Conn, cfg *ssh.ServerConfig, shellPath string, logger *log.Logger) {
	defer c.Close()
	_, chans, reqs, err := ssh.NewServerConn(c, cfg)
	if err != nil {
		logger.Printf("handshake from %s: %v", c.RemoteAddr(), err)
		return
	}
	go ssh.DiscardRequests(reqs)
	for nc := range chans {
		if nc.ChannelType() != "session" {
			nc.Reject(ssh.UnknownChannelType, "session only")
			continue
		}
		ch, requests, err := nc.Accept()
		if err != nil {
			logger.Printf("accept channel: %v", err)
			continue
		}
		go serveSession(ch, requests, shellPath, logger)
	}
}

type execPayload struct {
	Command string
}

func serveSession(ch ssh.Channel, requests <-chan *ssh.Request, shellPath string, logger *log.Logger) {
	defer ch.Close()
	var cmd *exec.Cmd
	for req := range requests {
		switch req.Type {
		case "exec":
			var p execPayload
			if err := ssh.Unmarshal(req.Payload, &p); err != nil {
				req.Reply(false, nil)
				continue
			}
			cmd = exec.Command(shellPath, "-c", p.Command)
			req.Reply(true, nil)
		case "shell":
			cmd = exec.Command(shellPath)
			req.Reply(true, nil)
		case "env", "window-change":
			req.Reply(true, nil)
		default:
			req.Reply(false, nil)
		}
	}
	if cmd == nil {
		return
	}
	cmd.Stdin = ch
	cmd.Stdout = ch
	cmd.Stderr = ch.Stderr()
	if err := cmd.Run(); err != nil {
		var ee *exec.ExitError
		if !errors.As(err, &ee) {
			logger.Printf("exec: %v", err)
		}
	}
	status := uint32(0)
	if ee, ok := cmd.ProcessState.Sys().(interface{ ExitStatus() int }); ok {
		if s := ee.ExitStatus(); s > 0 {
			status = uint32(s)
		}
	}
	_, _ = ch.SendRequest("exit-status", false, ssh.Marshal(struct{ S uint32 }{status}))
}
