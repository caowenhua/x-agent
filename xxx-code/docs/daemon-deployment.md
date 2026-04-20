# xxx-code Daemon Deployment

## 现成模板

仓库里现在直接提供了几套可以拿来改的部署模板：

- `deploy/systemd/xxx-code.service`
- `deploy/launchd/io.github.csonxx.xxx-code-daemon.plist`
- `deploy/docker/Dockerfile`
- `deploy/docker/compose.yaml`
- `deploy/docker/config.yaml.example`

推荐做法不是原样照搬，而是把里面的：

- 二进制路径
- 配置路径
- 工作目录
- token file 路径
- 日志路径

改成你自己机器或环境的实际布局。

## 最小暴露面

- 默认继续监听 `127.0.0.1:7331`
- 非必要不要直接把 daemon 端口暴露到公网
- 优先通过反向代理、SSH tunnel、Tailscale 或内网入口访问
- bearer token 放在独立 secret 文件里，并限制权限到 `0600`

## Token 轮换

`xxx-code` 现在支持：

- `--daemon-token-file`
- `--remote-token-file`

daemon 会在每次 `/v1/*` 请求时重新读取 token file，remote bridge 也会在每次请求时重新读取 token file，所以轮换不需要重启。

token file 支持这些格式：

```text
single-token
```

```text
new-token
old-token
```

```json
["new-token", "old-token"]
```

```json
{"tokens":["new-token","old-token"]}
```

推荐轮换流程：

1. daemon token file 先写成 `["new-token","old-token"]`
2. remote/client token file 更新成 `new-token`
3. 验证所有 client 都已经切到新 token
4. daemon token file 最后收敛成 `["new-token"]`

## 反向代理

最简单的安全边界通常是：

```text
client -> TLS reverse proxy -> xxx-code daemon (127.0.0.1)
```

重点是：

- TLS 在反向代理层终止
- daemon 仍然只绑定 loopback
- proxy 只把必要路径转发到 `/v1/*`
- 仍然保留 daemon 自己的 bearer token，不要只依赖外层网络边界

## Caddy 示例

```caddyfile
agent.example.com {
  encode zstd gzip
  reverse_proxy 127.0.0.1:7331
}
```

## Nginx 示例

```nginx
server {
  listen 443 ssl http2;
  server_name agent.example.com;

  ssl_certificate     /etc/letsencrypt/live/agent/fullchain.pem;
  ssl_certificate_key /etc/letsencrypt/live/agent/privkey.pem;

  location / {
    proxy_pass http://127.0.0.1:7331;
    proxy_http_version 1.1;
    proxy_set_header Host $host;
    proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
    proxy_set_header X-Forwarded-Proto $scheme;
  }
}
```

## 推荐组合

对外提供 daemon 时，推荐至少满足下面 4 条：

1. `--daemon-token-file` 开启
2. `--daemon-audit-file` 开启
3. `--daemon-allow-modes` / `--daemon-allow-session-prefix` 限制访问面
4. `--daemon-rate-limit-per-minute` / `--daemon-rate-limit-burst` 开启

如果这台 daemon 会长期跑，建议再补上：

5. `--daemon-metrics` 开启
6. 排障时按需临时开启 `--daemon-pprof`

一个更接近生产的例子：

```bash
go run ./cmd/xxx-code \
  --daemon \
  --listen 127.0.0.1:7331 \
  --daemon-token-file .secrets/daemon-token.json \
  --daemon-audit-file .xxx-code/daemon/audit.jsonl \
  --daemon-metrics \
  --daemon-allow-modes sessions_read,sessions_write,turns,agents,workflows,audit \
  --daemon-allow-session-prefix team- \
  --daemon-rate-limit-per-minute 120 \
  --daemon-rate-limit-burst 20
```

## Metrics 与 pprof

`xxx-code` 现在支持两个诊断入口：

- `/metrics`
- `/debug/pprof/*`

这两个入口都不走单独的安全逻辑，而是直接复用 daemon 现有的：

- bearer token
- `daemon_allow_modes` / `daemon_deny_modes`
- `introspection` ACL mode

也就是说，如果你配置了 token，那么抓 metrics 和 pprof 也必须带 token；如果你把 `introspection` 从 allowlist 里去掉，这两个入口也会一起被拒绝。

推荐做法：

- `--daemon-metrics` 常开
- `--daemon-pprof` 只在排障窗口开启，问题定位完就关闭
- 如果确实需要长期保留 `pprof`，至少放在内网、SSH tunnel 或反向代理之后

抓 metrics 示例：

```bash
curl -H "Authorization: Bearer $(cat /etc/xxx-code/secrets/daemon-token.txt)" \
  http://127.0.0.1:7331/metrics
```

抓 heap profile 示例：

```bash
curl -H "Authorization: Bearer $(cat /etc/xxx-code/secrets/daemon-token.txt)" \
  http://127.0.0.1:7331/debug/pprof/heap > /tmp/xxx-code-heap.pb.gz

go tool pprof -http=:0 /tmp/xxx-code-heap.pb.gz
```

如果你使用的是受保护的 daemon，可以先通过本地隧道暴露，再在隧道内抓取 profile。

当前 `/metrics` 里已经包含：

- daemon HTTP request / error 计数
- turn / tool latency 汇总
- agent lifecycle 事件计数
- 当前 session / agent / workflow 状态分布
- Go runtime goroutine / heap / GC 指标

## systemd 模板

Linux 主机上，最推荐的方式通常是：

1. 把二进制放到 `/usr/local/bin/xxx-code`
2. 把配置放到 `/etc/xxx-code/config.yaml`
3. 把 token file 放到 `/etc/xxx-code/secrets/daemon-token.txt`
4. 把运行状态放到 `/var/lib/xxx-code`
5. 把日志放到 `/var/log/xxx-code`

模板文件：

- `deploy/systemd/xxx-code.service`

典型步骤：

```bash
sudo useradd --system --home /var/lib/xxx-code --shell /usr/sbin/nologin xxx-code
sudo mkdir -p /etc/xxx-code /etc/xxx-code/secrets /var/lib/xxx-code /var/log/xxx-code
sudo install -m 0755 ./bin/xxx-code /usr/local/bin/xxx-code
sudo install -m 0644 ./deploy/systemd/xxx-code.service /etc/systemd/system/xxx-code.service
sudo install -m 0600 ./.secrets/daemon-token.txt /etc/xxx-code/secrets/daemon-token.txt
sudo install -m 0644 ./examples/config.yaml /etc/xxx-code/config.yaml
sudo chown -R xxx-code:xxx-code /var/lib/xxx-code /var/log/xxx-code
sudo systemctl daemon-reload
sudo systemctl enable --now xxx-code
```

常用命令：

```bash
sudo systemctl status xxx-code
sudo journalctl -u xxx-code -f
```

## launchd 模板

macOS 上可以用 `launchd` 托管 daemon。

模板文件：

- `deploy/launchd/io.github.csonxx.xxx-code-daemon.plist`

典型步骤：

```bash
mkdir -p /usr/local/etc/xxx-code /usr/local/etc/xxx-code/secrets /usr/local/var/lib/xxx-code /usr/local/var/log/xxx-code
install -m 0755 ./bin/xxx-code /usr/local/bin/xxx-code
install -m 0644 ./deploy/launchd/io.github.csonxx.xxx-code-daemon.plist ~/Library/LaunchAgents/io.github.csonxx.xxx-code-daemon.plist
install -m 0600 ./.secrets/daemon-token.txt /usr/local/etc/xxx-code/secrets/daemon-token.txt
install -m 0644 ./examples/config.yaml /usr/local/etc/xxx-code/config.yaml
launchctl unload ~/Library/LaunchAgents/io.github.csonxx.xxx-code-daemon.plist 2>/dev/null || true
launchctl load ~/Library/LaunchAgents/io.github.csonxx.xxx-code-daemon.plist
launchctl start io.github.csonxx.xxx-code-daemon
```

查看状态：

```bash
launchctl print gui/$(id -u)/io.github.csonxx.xxx-code-daemon
```

## Docker 模板

如果你希望把 daemon 跑在容器里，仓库里也提供了一套最小模板：

- `deploy/docker/Dockerfile`
- `deploy/docker/compose.yaml`
- `deploy/docker/config.yaml.example`

典型步骤：

```bash
cd deploy/docker
cp config.yaml.example config.yaml
mkdir -p secrets
printf 'replace-me\n' > secrets/daemon-token.txt
docker compose up --build -d
```

默认 compose 模板会：

- 将仓库根目录挂到容器内的 `/workspace`
- 把 daemon 端口暴露到 `127.0.0.1:7331`
- 把状态目录和日志目录做成 named volume

如果你的目标不是操作当前仓库，而是操作另一份代码，请把 compose 里的 workspace volume 改成你自己的目标目录。
