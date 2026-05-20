# napcat-jm-go

一个面向 NapCat/OneBot 事件的 Go 机器人，提供：
- `JM` 本子下载、检索、发送（PDF/ZIP）
- `哔咔` 漫画源支持（原画下载、自动升级）
- 识图（soutubot）联动检索并回填 `JM` 号
- 文件加密、随机密码、命名策略、去重与队列控制
- 内置/外置 Cloudflare bypass 对接
- 一键 `systemd` 安装与卸载

## 1. 项目结构

```text
.
├── cmd/napcat-jm-go/        # 标准 Go 入口
├── internal/app/            # 核心实现（HTTP事件、命令、下载、识图、发送、AI画图）
├── internal/aiimage/        # AI 画图 OpenAI 兼容 API 客户端
├── configs/
│   ├── config.example.yml   # 示例配置
│   └── option.yml           # jmcomic 配置
├── docs/
│   └── README_GO.md         # 历史说明（保留）
├── bin/                     # 构建产物
├── config.yml               # 实际运行配置（本地）
├── go.mod / go.sum
└── main.go                  # 兼容入口（支持 `go run .`）
```

说明：
- 推荐入口：`go run ./cmd/napcat-jm-go` 或 `bin/napcat-jm-go`
- 兼容入口：`go run .`（仍可用）

## 2. 核心能力

### 2.1 命令能力
**基础命令：**
- `/jm <ID>`：下载并发送
- `/jm look <ID>`：查看本子信息
- `/jm search <关键词>`：搜索本子（聚合搜索哔咔+JM）
- `搜索 <关键词>`：同上
- `/jm search` / `识图` / `/jm识图`：进入识图窗口（默认 120 秒）
- `/jm goodluck` / `/goodluck` / `随机本子`：随机本子
- `/jm help`：查看帮助

**确认下载**：
- `确认 1` 或 `1` - 下载第1个结果
- `确认 1 2 3` 或 `1 2 3` - 批量下载

**用户设置：**
- `/jm mode pdf|zip`：发送格式
- `/jm enc on|off`：加密开关
- `/jm passwd <密码>`：设置加密密码
- `/jm randpwd on|off`：随机密码加密
- `/jm fname jm|full|current`：文件命名方式
- `/jm regex on|off`：正则提取模式
- `/jm strict on|off`：严格模式（只处理/jm开头的消息）

**管理员命令：**
- `/jm admin`：私聊认领管理员
- `/jm on|off`：启用/禁用当前群
- `/jm addban <ID>`：封禁本子ID
- `/jm delban <ID>`：解封本子ID
- `/jm setmax <数字>`：设置章节数阈值
- `/jm cfg list|show|set`：在线配置管理
- `/jm dedup show|set|clear`：重复请求冷却管理
- `/jm daily on|off|now`：每日推荐开关/立即发送
- `/jm daily add|del <群号>`：管理推荐群

### 2.2 严格模式
开启后只处理以 `/jm` 开头的消息，避免普通聊天触发本子下载：
```
/jm strict on    # 开启
/jm strict off   # 关闭
```

**示例：**
```
/jm 123456,789012   → 正常下载
普通聊天消息        → 忽略
/jm 本子号1，本子号2(一段字符串)本子号3 → 提取多个本子号下载
```

### 2.3 哔咔漫画源
- `/bika on|off`：启用/关闭哔咔（管理员）
- `/bika login <邮箱> <密码>`：登录哔咔账号
- `/bika logout`：退出当前账号
- `/bika whoami`：查看当前登录状态
- `/bika search <关键词>`：搜索漫画
- `/bika look <ID>`：查看漫画详情
- `/bika dl <ID> [章节]`：下载漫画
- `/bika confirm <序号>`：确认搜索结果下载
- `/bika help`：查看哔咔帮助

**自动升级策略**：当用户请求JM本子时，系统会自动在哔咔搜索匹配内容，如果找到就从哔咔下载原画版。

**登录机制**：
- 管理员登录后自动设为全局默认账号
- 其他用户可登录自己的账号
- 未登录用户使用全局账号

### 2.4 聚合搜索
使用 `搜索 <关键词>` 或 `/jm search <关键词>` 会同时搜索哔咔和JM，结果以 `[Bika]` 或 `[JM]` 标记来源。

### 2.5 每日推荐
- `/jm daily on`：启用每日推荐
- `/jm daily off`：关闭每日推荐
- `/jm daily add <群号>`：添加推荐群
- `/jm daily del <群号>`：删除推荐群
- `/jm daily now`：立即发送推荐

每日推荐发送哔咔日榜（优先）或JM热门本子，用户可回复序号下载。

### 2.6 识图联动
- 识图成功后，会自动提取标题关键词（含中日文片段）并走 `/jm search` 同款检索逻辑
- 命中后自动写入待确认队列，回复"确认"即可下载

### 2.7 AI 画图
- `image on`：开启 AI 画图（管理员）
- `image off`：关闭 AI 画图
- `image2 <提示词>`：生成图片
- **图生图**：回复带图片的消息发送 `image2 <提示词>`，可将回复的图片作为参考图
- 失败自动重试最多 3 次
- 配置项：`ai_image_enabled`、`ai_image_api_key`、`ai_image_base_url`、`ai_image_model`、`ai_image_size`、`ai_image_timeout`、`ai_image_max_retries`
- API Key 支持 `AI_IMAGE_API_KEY` 环境变量回退，避免写入配置文件

### 2.8 发送策略
- 本子以转发卡片形式发送（信息+封面+文件）
- 文件通过QQ正式上传，聊天记录中可永久打开
- CBZ文件本地保留，PDF发送后延迟24小时删除

## 3. 配置说明

主配置文件：`config.yml`  
示例模板：`configs/config.example.yml`

首次运行若没有 `config.yml`，程序会自动生成“最小配置模板”并退出，提示你先填写关键项后再启动：
- `admin_id`
- `websocket_url`
- `websocket_token`

管理员初始化说明：
- 若 `admin_id: 0`（未设置），可由首个私聊机器人发送 `/jm admin` 的账号自动认领管理员。

关键配置项（建议优先检查）：
- `http_host` / `http_port`：NapCat 回调监听地址
- `preview_host` / `preview_port`：本地 CBZ 预览服务地址（默认 `::` / `3502`，支持 `http://域名:3502/350234`）
- `preview_public_base_url`：文件发送失败时补发的在线预览链接前缀（可填 `https://jm.zuichen.top:3502`）
- `http_port_fallback`：主端口是否自动退避（默认 `false`，建议保持）
- `websocket_url` / `websocket_token`：NapCat WS 发送通道
- `transfer_mode`：默认建议 `local`
- `remote_temp_dir`：支持 `${USER}` 变量，如 `/tmp/napcat-jm-go-${USER}/temp`
- `jm_option_path`：默认 `./configs/option.yml`
- `jm_proxy`：JM下载代理（如 `socks5://127.0.0.1:1080`）
- `file_dir` / `manga_dir` / `cbz_dir`
- `soutu_*`：识图请求参数
- `cf_bypass_api_url`：外置 bypass 地址（推荐生产使用）
- `embedded_bypass_enabled`：是否启用内置 bypass（需要本机 chrome/chromium）

**模式配置**：
- `regex_enabled_global`：正则模式（默认 `true`）
- `strict_mode_global`：严格模式（默认 `false`）
- `max_concurrent_downloads`：并发下载数（默认 `3`）

**哔咔配置**：
- `bika_enabled`：是否启用哔咔（默认 `false`）
- `bika_base_url`：哔咔 API 地址（默认 `https://picaapi.picacomic.com/`）
- `bika_token`：登录 token（管理员登录后自动设置）
- `bika_quality`：图片画质（`original` / `high` / `middle` / `low`）
- `bika_proxy`：HTTP 代理（如 `http://127.0.0.1:1081`）

**每日推荐配置**：
- `daily_recommend_enabled`：是否启用（默认 `false`）
- `daily_recommend_hour`：发送时间-小时（默认 `0`）
- `daily_recommend_minute`：发送时间-分钟（默认 `0`）
- `daily_recommend_groups`：发送群列表

## 4. 运行方式

### 4.1 开发运行
```bash
go mod tidy
go run .
```

或标准入口：
```bash
go run ./cmd/napcat-jm-go
```

### 4.2 构建运行
```bash
go build -o bin/napcat-jm-go ./cmd/napcat-jm-go
./bin/napcat-jm-go
```

### 4.3 Docker 运行

使用 `Dockerfile`：

```bash
docker build -t napcat-jm-go:latest .
docker run -d --name napcat-jm-go \
  -p 8071:8071 -p 18000:18000 \
  -v $(pwd)/config.yml:/app/config.yml \
  -v $(pwd)/pdf:/app/pdf \
  -v $(pwd)/manga:/app/manga \
  -v $(pwd)/cbz:/app/cbz \
  -v $(pwd)/logs:/app/logs \
  napcat-jm-go:latest
```

查看日志：
```bash
docker logs -f napcat-jm-go
```

停止并删除容器：
```bash
docker rm -f napcat-jm-go
```

### 4.4 Docker Compose（推荐）

项目已提供 `docker-compose.yml`：

```bash
docker compose up -d --build
docker compose logs -f
docker compose down
```

默认挂载：
- `./config.yml -> /app/config.yml`
- `./pdf -> /app/pdf`
- `./manga -> /app/manga`
- `./cbz -> /app/cbz`
- `./logs -> /app/logs`

**AI 画图配置**：
- `ai_image_enabled`：是否启用 AI 画图（默认 `false`，管理员发送 `image on` 后自动激活）
- `ai_image_api_key`：API 密钥（建议使用环境变量 `AI_IMAGE_API_KEY`）
- `ai_image_base_url`：OpenAI 兼容 API 地址（默认 `http://47.104.6.123:3000/v1`）
- `ai_image_model`：模型名称（默认 `gpt-image-2`）
- `ai_image_size`：图片尺寸（默认 `1024x1024`）
- `ai_image_timeout`：请求超时秒数（默认 `300`，5 分钟）
- `ai_image_max_retries`：失败重试次数（默认 `3`）
- `ai_image_waiting_image`：等待时发送的图片（URL 或 `base64://`，空则不发送）

## 5. NapCat 配置（必须）

本项目需要 NapCat 同时配置两条通道：
- `WebSocket 服务端`：供本项目主动发送消息（本项目作为 WS 客户端连接）
- `HTTP 客户端`：NapCat 将事件回调到本项目（本项目作为 HTTP 服务端接收）

### 5.1 在 NapCat 创建 WebSocket 服务端

在 NapCat 后台新增一个 OneBot WS 服务端（名字可自定义）：
- 监听地址示例：`0.0.0.0`
- 端口示例：`13001`
- Access Token：例如 `1`（建议生产使用强随机字符串）

然后在本项目 `config.yml` 对应填写：

```yaml
websocket_url: "ws://127.0.0.1:13001"
websocket_token: "1"
```

说明：
- 如果项目与 NapCat 同机，`127.0.0.1` 最稳。
- 若跨机器部署，把 `127.0.0.1` 改成 NapCat 机器 IP。

### 5.2 在 NapCat 创建 HTTP 客户端

在 NapCat 后台新增一个 OneBot HTTP 客户端（上报地址）：
- 上报 URL 示例：`http://127.0.0.1:8071/`
- 请求方法：`POST`
- 若 NapCat 支持上报密钥，请与本项目保持一致（当前本项目主要使用 WS Token）

然后在本项目 `config.yml` 对应填写：

```yaml
http_host: "0.0.0.0"
http_port: 8071
http_port_fallback: false
```

说明：
- `http_port_fallback` 建议保持 `false`，避免端口漂移导致 NapCat 回调失效。
- 若端口冲突，请先释放占用进程后再启动本项目。

### 5.3 联调自检清单

1. 启动项目后看到日志：
   - `go bot listening at ...:8071`
2. NapCat 发送任意消息到机器人后，项目进程无报错
3. 执行 `/jm help` 能收到回复
4. 识图链路测试：
   - 发送 `/jm search`
   - 120 秒内发送一张图片

若失败，优先检查：
- NapCat HTTP 客户端 URL 是否写成 `http://<项目IP>:8071/`
- `websocket_url` / `websocket_token` 是否与 NapCat WS 服务端一致
- 8071 端口是否被其他进程占用
## 6. systemd 管理

程序内置安装/卸载参数：

```bash
# 安装并启动（需 root）
sudo ./bin/napcat-jm-go --install

# 卸载（停用+删除服务，需 root）
sudo ./bin/napcat-jm-go --uninstall
```

可选参数：
- `--service-name`（默认 `napcat-jm-go`）
- `--service-user`（默认当前登录用户 / `SUDO_USER`）
- `--service-group`（默认用户主组）

## 7. 识图链路说明

识图输入会按以下顺序解析：
1. `image.base64`
2. `image.url`
3. `image.file` -> 调用 OneBot `get_image` 换取 URL
4. CQ / HTML 回退字段提取（兼容场景）

Cloudflare 处理：
- 优先走现有 cookie
- 命中拦截后调用 `cf_bypass_api_url` 轮询获取 `cf_clearance`
- 推荐使用外置 bypass 服务，稳定且无需本机浏览器

## 8. 常见问题

### Q1: 启动报端口占用
报错示例：`bind: address already in use`

处理：
```bash
ss -ltnp | grep ':8071 '
kill <pid>
```
或修改 `config.yml` 中 `http_port`。

### Q2: 识图失败，提示 bypass/chrome 问题
- 若使用内置 bypass：需安装 `chrome/chromium`
- 若不想装浏览器：将 `embedded_bypass_enabled: false`，并配置可用 `cf_bypass_api_url`

### Q3: 回复“确认”未触发下载
- 必须在 `search_timeout` 窗口内回复
- 必须在同一会话范围（当前已按用户级 scope 处理）

### Q4: 发了图但没进入识图
- 先看日志中的 `[soutu-debug]` 行（已内置）
- 重点看是否命中 armed 窗口、是否提取到图片源、`get_image` 是否成功

## 9. 日志与调试

日志文件保存在 `logs/` 目录，按日期命名：`logs/bot_2026-05-12.log`

识图相关调试日志前缀：
- `[soutu-debug] recv event`
- `[soutu-debug] armed ...`
- `[soutu-debug] extracted sources ...`
- `[soutu-debug] get_image ...`
- `[soutu-debug] search success ...`

哔咔相关调试日志前缀：
- `[Bika] 检查哔咔升级条件`
- `[Bika] 搜索关键词`
- `[Bika] 搜索结果`
- `[Bika] 下载成功/失败`

如果需要排障，建议贴出同一时段完整日志片段（含 group/user/scope 行）。

## 10. 安全与运维建议

- `config.yml` 不入库（已在 `.gitignore`）
- `enc_password_*` 建议使用强密码并定期轮换
- 生产建议用 `systemd` + 外置 bypass API
- 主端口建议固定（`http_port_fallback: false`），避免 NapCat 回调漂移

## 11. 更新日志

### 2026-05-20 v3 — 错误信息优化与参数调整

- **已推送至 `hongyue0721/jmbot` 和 `zuichen123/jmbot` 的 `image` 分支**（PR #7）
- 失败时不再暴露详细 API 错误，改为显示 `<提示词> 图片生成失败`（详情写入服务端日志）
- 默认超时改为 `300s`（5 分钟），默认重试次数改为 `2` 次
- 等待图片改用动画 GIF（Wikipedia ajax-loader），替换原来的 PNG 旋转 spinner
- 默认等待图通过 `base64://` 直接内嵌，无需额外网络请求

### 2026-05-20 v2 — 新增 AI 画图插件

以同进程插件方式集成 AI 图像生成，不修改 JM/哔咔/下载/识图等核心逻辑。

**新增指令**

| 指令 | 说明 | 权限 |
|------|------|------|
| `image on` | 开启 AI 画图功能 | 管理员 |
| `image off` | 关闭 AI 画图功能 | 管理员 |
| `image2 <提示词>` | 根据文字生成图片 | 所有人 |
| 回复图片 + `image2 <提示词>` | 以回复的图片为参考进行图生图 | 所有人 |

**功能说明**
- 图生图：引用带图片的消息发送 `image2 <提示词>`，自动提取引用图片作为参考，调用 `/v1/images/edits` 端点
- 若模型不支持图生图（如 `gpt-image-2`），自动降级为文生图并提示用户
- 失败自动重试（默认 3 次），错误信息从 API 响应体中提取而非仅返回状态码
- 提取引用图片时先查 `url`/`base64`，若无则通过 `get_image` 解析 `file` UUID
- 生成时先发送等待图片（四色旋转 spinner），结果生成后发送最终图片
- 引用图片提取失败写日志，不再静默降级

**新增配置项**

```yaml
ai_image_enabled: false              # 是否启用 AI 画图
ai_image_base_url: "https://api.openai.com/v1"  # API 地址
ai_image_api_key: ""                 # API Key（也可通过环境变量 AI_IMAGE_API_KEY 设置）
ai_image_model: "dall-e-3"           # 模型名
ai_image_size: "1024x1024"           # 图片尺寸
ai_image_timeout_seconds: 300        # 请求超时（秒），默认 5 分钟
ai_image_max_retries: 3              # 失败重试次数
ai_image_waiting_image: ""           # 等待图片（URL 或 base64://，空则使用默认 spinner）
```

**新增文件**
- `internal/aiimage/aiimage.go` — OpenAI 兼容 API 客户端（文生图/图生图/重试/错误提取）
- `internal/app/ai_image.go` — App 命令处理层（消息解析/图片提取/结果发送）

**修改文件**
- `internal/app/main.go` — Config 新增 7 个字段 + fillDefaults + SendGroupImage + GetMsg/SendGroupMsgWithAtAndImage/SendPrivateMsgWithImage/SendGroupMsgWithAtText
- `configs/config.example.yml` — AI 画图配置示例
- `README.md` — 本更新日志

### 2026-05-20 v1 — 修复引用图生图

- 修复 `extractAIImageBytes` 对回复消息缺少 `file` ref 回退的问题，补充 `extractSoutuImageFileRefsFromEvent` 路径
- API 错误不再只返回状态码，改为从响应体提取 `error.message`
- 提取失败不再静默忽略，写日志记录
- 新增 `ai_image_waiting_image` 支持生成等待图
