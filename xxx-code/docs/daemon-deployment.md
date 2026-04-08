# xxx-code Daemon Deployment

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

一个更接近生产的例子：

```bash
go run ./cmd/xxx-code \
  --daemon \
  --listen 127.0.0.1:7331 \
  --daemon-token-file .secrets/daemon-token.json \
  --daemon-audit-file .xxx-code/daemon/audit.jsonl \
  --daemon-allow-modes sessions_read,sessions_write,turns,agents,workflows,audit \
  --daemon-allow-session-prefix team- \
  --daemon-rate-limit-per-minute 120 \
  --daemon-rate-limit-burst 20
```
