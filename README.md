# phone-rescue

Аварийный SSH-доступ к Redmi 9a **независимо от Termux**: статический sshd в `/data/local/rescue`,
запускается root через Magisk, слушает только локальный IP Wi-Fi, пускает только по ключу.

## Установка на телефоне (root-терминал)
```sh
# 1) скачать готовый бинарь (без сборки на телефоне)
su -c 'mkdir -p /data/local/rescue && chmod 700 /data/local/rescue'
su -c 'curl -fsSL -o /data/local/rescue/rescue-arm https://github.com/Zenoku-Dev/phone-rescue/releases/download/rel-20261121/rescue-arm && chmod 755 /data/local/rescue/rescue-arm'

# 2) ключ доступа (публичный, с dev)
su -c "echo 'ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIIhfV29HLrP2ZkHew3Uox6N6QUOpcPzoV8C8aDAH04r5 zenoku-rescue-phone' > /data/local/rescue/authorized_keys && chmod 600 /data/local/rescue/authorized_keys"

# 3) автозапуск при загрузке (Magisk service.d)
su -c "cat > /data/adb/service.d/zenoku-rescue <<'EOS'
#!/system/bin/sh
/system/bin/sleep 10
/data/local/rescue/rescue-arm >>/data/local/rescue/rescue.log 2>&1 &
EOS
chmod 755 /data/adb/service.d/zenoku-rescue"

# 4) запустить сейчас
su -c '/data/local/rescue/rescue-arm >>/data/local/rescue/rescue.log 2>&1 &'
su -c 'cat /data/local/rescue/rescue.log | tail -3'
```

## Подключение с dev (в той же Wi-Fi)
```sh
ssh -i ~/.ssh/zenoku-rescue -p 2222 root@192.168.0.146 'id; uptime'
# или интерактивно
ssh -i ~/.ssh/zenoku-rescue -p 2222 root@192.168.0.146
```

## Важно
- Слушает только приватный IP (192.168.x / 10.x / 172.16.x), не публичный/мобильный.
- Пароль отключён — только публичный ключ. Хост-ключ создаётся сам при первом старте.
-救命: из этого шелла можно починить Termux backup/bootstrap, не теряя доступ к телефону.
