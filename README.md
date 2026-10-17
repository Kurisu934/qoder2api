# qoder2api

轻量级 [Qoder](https://qoder.ai) → OpenAI / Claude / Codex 兼容 API 网关。

仅包含 **Web 控制台 + Bridge**，无 macOS 桌面 GUI、无 Wails、无系统托盘，适合 Linux 服务器与本地轻量部署。

本项目基于 [wangtufly/QCCG](https://github.com/wangtufly/QCCG) **二次开发**，抽取 Bridge / 账号 / 签名核心逻辑，重做成可服务器部署的精简版本。

本 Fork：[Kurisu934/qoder2api](https://github.com/Kurisu934/qoder2api)，基于 [Zhengyuuuui/qoder2api](https://github.com/Zhengyuuuui/qoder2api)。

## Fork 修正：v2026.10.18-kimi.2

- 修复上下文档位解析错误：Kimi-K3 的原始目录顶层是 `max_input_tokens: 180000`，但 `context_config` 实际提供 `200K`（默认）、`400K`、`1M`。此前优先保留顶层值，错误地把默认输入值当成模型最大上下文。
- 现在按每个模型自身目录选择最大的已启用档位，K3 的公开 `context_window` / `max_input_tokens` 及原生请求的 `model_config.max_input_tokens` / `parameters.context_length` 均为 **1,000,000**；不是给所有模型硬编码 1M。禁用档位不参与选择，没有可用档位才保留顶层元数据。
- 验证使用只读原生目录和隔离上游：修复前公开值及签名请求体均为 180000，修复后均为 1000000；另覆盖最大档位禁用、扁平配置和无档位回退。现有 bridge 测试及 race-enabled 冒烟通过；没有发送真实推理请求。

## Fork 更新：v2026.10.18-kimi.1

- **按模型名调用**：`GET /v1/models` 的 `id` 使用实时目录显示名，例如 `Auto`、`Kimi-K3`。三种协议均按完整名称解析（不区分大小写），不再按 GPT/Claude 子串猜测或接受任意内部 key。未知、禁用、同名不同 SKU 的模型返回 400；不会静默换成另一款模型。
- **K3 思考**：Chat 的 `reasoning_effort`、Responses 的 `reasoning.effort` 和 Claude 的 `thinking.type`（`enabled` / `adaptive` / `disabled`）、`output_config.effort` 转为原生 `parameters.reasoning_effort`。未指定 effort 的 enabled/adaptive 使用 `high`，disabled 优先映射为 `none`；`minimal` 映射为 `low`，`xhigh` 与 `max` 保持原值。`thinking.budget_tokens` 不对应已确认的原生预算字段，不能保证逐 token 预算；需要强度控制时使用 effort。
- **能力与默认模式分离**：Kimi-K3 公开 `supports_reasoning=true`，但保留目录的 `default_reasoning=false`，原生 `model_config.is_reasoning` 不被客户端开关覆盖。已观察到可工作的 K3 请求使用该 false 值加独立 `reasoning_effort`。实际收到的思考内容不再在转换时丢弃。
- **输出与上下文**：转发 Responses `max_output_tokens`、Chat `max_completion_tokens` / `max_tokens` 和 Claude `max_tokens`；按所选模型目录元数据设置上下文，不继承 Auto 的较小容量。目录未给出的限制不编造。
- **无状态 Responses**：流式与非流式共享聚合，支持文本/图片、developer/instructions、函数工具及其历史回传、并行调用参数分片、reasoning summary 和实际 usage（含缓存/思考 token 明细）。SSE 提供单调序号及 item/part/delta/done 生命周期，终态包含完整 output；没有 usage 时返回 null。
- **Responses 真实终态**：有原生终止信号才完成；达到长度上限返回 incomplete。裸 EOF、错误、取消、缺失工具标识不会伪报 completed；仅有 `event: finish` 头还不算完整 SSE 事件，必须读完其数据和分隔行。`previous_response_id` / conversation、`store=true`、`background=true`、非函数工具及加密 reasoning 历史明确返回 400；调用方须携带完整历史。
- **验证边界**：现有 bridge 测试及 race-enabled 本地合成上游冒烟通过，覆盖上述协议与签名请求体、分片工具历史、终态和取消；未调用真实模型，不能据此宣称已修复上游 K3 的短答/提前停止。

显式别名仍可配置为 `model_mappings[agent][客户端别名] = 目录显示名或内部 key`，但不会作为额外公共模型条目发布。旧的扁平 `model_mapping` 已移除；普通接入无需配置任何映射。账号目录缓存按账号/区域共用，保持现有 30 分钟 TTL、重试次数与冷却策略；不修改 prompt cache marker。

[![Go](https://img.shields.io/badge/Go-1.22+-00ADD8?logo=go)](https://go.dev)
[![License](https://img.shields.io/badge/License-见原项目-blue)](https://github.com/wangtufly/QCCG/blob/main/LICENSE)

---

## 🙏 特别致谢 · 贡献者

本项目的海外版签到、多账号池、图片输入等核心特性由社区贡献者 **[LeonardW-sl](https://github.com/LeonardW-sl)** 主导实现，特此致以诚挚谢意。

| 贡献 | PR | 说明 |
|------|-----|------|
| **国际版（Global）签到支持** | [`b84c41b`](https://github.com/Zhengyuuuui/qoder2api/commit/b84c41b) | 新增 `Endpoints.OpenAPIHost`，签到域名按账号 `region` 自动路由（`cn` → `openapi.qoder.com.cn`，`global` → `openapi.qoder.sh`），修复国际账号 token 打到国内端点导致 `401 TOKEN_EXPIRE` 的问题；并补充 `TestCheckinHostForRegion` 单元测试 |
| **每日自动签到时间可配置** | [`fcde08e`](https://github.com/Zhengyuuuui/qoder2api/commit/fcde08e) | 新增 `auto_checkin_times`，支持多时段签到（`HH:MM` 列表，留空默认 `10:00`） |
| **多账号池** | [`c5fa3fa`](https://github.com/Zhengyuuuui/qoder2api/commit/c5fa3fa) ~ [`afe1d11`](https://github.com/Zhengyuuuui/qoder2api/commit/afe1d11) | 账号轮换、冷却、故障转移、状态持久化、池状态接口与控制台可视化 |
| **图片输入（多模态）** | [`63cef2a`](https://github.com/Zhengyuuuui/qoder2api/commit/63cef2a) | 支持 `image_url` 类型的多模态请求 |
| OAuth 会话 TTL 可配置 | [`624ccf6`](https://github.com/Zhengyuuuui/qoder2api/commit/624ccf6) | 新增 `QODER2API_OAUTH_TIMEOUT` 环境变量 |

感谢 [@LeonardW-sl](https://github.com/LeonardW-sl) 的持续贡献与代码质量（含单元测试覆盖）。欢迎更多社区朋友参与共建。

### 其他贡献者

| 贡献者 | 贡献 | 说明 |
|------|-----|------|
| [@SDCOM-0415](https://github.com/SDCOM-0415) | **定位 `--instance=global` 端口被覆盖 Bug** | 发现 `ensureConsolePassword()` 首次启动写盘默认 `port:8963` 覆盖 `--instance=global` 预设的 `8964`，并给出修复方向（见[更新记录](#-修复-instanceglobal-的-bridge-端口被-settings-默认值冲成-8963)） |

---

## ✨ 新功能：每日签到领 100 Credits

控制台「账号列表 / 额度」区域内置一键签到，可将 Qoder 官方 **每日 100 Credits** 活动直接打进控制台：

- **🎁 一键签到**：顶部按钮批量对所有账号签到（CN 与 Global 账号可混在同一实例）
- **🎁 单账号签到**：账号表格「操作」列独立签到按钮
- **⏰ 每日自动签到**：可选开关（**默认关闭**），开启后每天 `10:00 (UTC+8)` 自动为全部账号签到；
  签到时间点可配置（支持多时段，如 `09:15,17:00`）
- **🔥 连续签到统计**：自动记录签到历史，跨天累计「连续 N 天 / 累计 N 天 / 共 N 积分」
- **幂等安全**：已领取自动跳过，重复点击不会重复领取

### 双区域支持

签到接口按账号 `region` 自动路由到对应站点，**同一控制台可同时管理国内版与国际版账号**：

| Region | `Endpoints.OpenAPIHost` | 签到接口 | `daily-check-in/status` |
|--------|-------------------------|---------|--------------------------|
| `cn` | `openapi.qoder.com.cn` | ✅ 可用 | `200`（legacy 已 `DISABLED`，统计恒 0） |
| `global` | `openapi.qoder.sh` | ✅ 可用 | `404`（该端点从未部署） |

实现由 [@LeonardW-sl](https://github.com/LeonardW-sl) 在 [`b84c41b`](https://github.com/Zhengyuuuui/qoder2api/commit/b84c41b) 贡献：`account.Endpoints` 新增 `OpenAPIHost` 字段，
`checkinAccount` 取 `checkinHostFor(acct.Region)` 后透传给 `campaignsCheckin` / `readDailyCheckinStats` / `doCheckinRequest`。

> 签到链路基于抓包还原，两区路径完全一致：
> `GET /sash/api/v1/me/campaigns` → `POST /sash/api/v1/me/campaigns/{id}/claim`，
> 仅需 device token + `cosy-clienttype: 10`，**无需 COSY 签名**。详见 `scripts/auto_checkin.py`。

### 实现说明

- **权威签到走 `campaigns`**：这是唯一真实发放积分的接口，返回 `grantId` 可核验到账
- **不依赖 `daily-check-in`**：该 legacy 端点在国内已 `DISABLED` 且对未领取日恒返回 `409`（会误判为已领取而漏领），海外则直接 `404`

#### ⚠️ 设备指纹政策（重要）

国际版（`qoder.com`）的签到活动**按真实设备身份下发**。缺指纹时服务端不下发
`CLAIM_BEFEFIT` 活动，表现为「无可用签到活动」（`no_campaign`）。
该结论由社区 A/B 实验验证（[issue #4](https://github.com/Zhengyuuuui/qoder2api/issues/4)，
对照 [yetone/magpie#1268](https://github.com/yetone/magpie/issues/1268)）：
补入 `cosy-machinetoken` + `cosy-machinetype` 后活动立即出现，移除任一字段活动消失。

**自造指纹无效**：`cosy-machinetoken` 由官方客户端本地生成，客户端无法自造（实测任意自造值均被拒）。
必须使用官方客户端的真实指纹。

| 区域 | 指纹要求 |
|------|---------|
| `cn`（`openapi.qoder.com.cn`） | 通常**不需要** |
| `global`（`openapi.qoder.sh`） | **首次领取前必须携带**真实指纹 |

**懒下发机制**：Global 账号在官方客户端**首次领取一次后**，服务端转为「懒下发」——
之后即使不带指纹，`campaigns` 也能查到该活动。所以新账号需先用官方客户端领一次，
或在 qoder2api 中配置指纹。

**配置方式**：在账号 JSON 中添加 `checkin_fingerprint` 字段：

```json
{
  "id": "acct_xxx",
  "name": "your-global-account",
  "region": "global",
  "checkin_fingerprint": {
    "machine_id": "6ba86f67-4909-4a9a-9567-c289d999f0c1",
    "machine_token": "<cosy-machinetoken>",
    "machine_type": "<cosy-machinetype>",
    "machine_code": "<可选，cosy-machinecode>",
    "hostname": "localhost",
    "machine_os": "aarch64_darwin",
    "version": "0.4.4"
  }
}
```

**获取指纹**：

```bash
# 自动读取 machine_id（明文文件），并给出 token/type 的获取指引
./scripts/extract_fingerprint.sh global
```

`machine_id` 存放在客户端明文中：
`~/Library/Application Support/com.qoder.app.stable/auth.machine-id`（macOS）

`machine_token` / `machine_type` 存于**加密**文件 `auth.v1.dat`（非标准密钥保护，无法离线解密），
需通过抓包获取：

```bash
# 1. 启动抓包
mitmdump -q -s scripts/capture_addon.py -p 8080
# 2. 开启代理
./scripts/proxy_toggle.sh on
# 3. 打开官方客户端 →「用量面板 → 礼物图标」触发活动加载
# 4. 关闭代理
./scripts/proxy_toggle.sh off
# 5. 从 scripts/capture/ 中取 URL 含 /sash/api/v1/me/campaigns 的请求头，
#    复制 cosy-machinetoken / cosy-machinetype / cosy-version
```

#### 其他行为

- **领取窗口**：`10:00 → 次日 09:59 (UTC+8)`，窗口未开启时点击会明确提示下次开放时间
- **连续天数本地统计**：上游 legacy 统计恒为 0，故由 qoder2api 记录 `checkin_history.json` 并按**活动窗口 `startAt` 日期**（而非自然日）计算，避免 10:00 前误记
- **调度容错**：签到时刻若活动尚未下发，不标记当日完成，每分钟重试至 12:00，避免漏签
- **多时段支持**：`auto_checkin_times` 可配置多个 `HH:MM` 时间点（贡献者 `fcde08e`）
- **排查辅助**：`no_campaign` 时日志会打印该账号是否带指纹、活动类型列表与原始响应，
  便于定位是「缺指纹」还是「服务端未下发」

**手动签到 API**：

```bash
# 所有账号一键签到
curl -X POST http://127.0.0.1:3588/api/checkin

# 指定单账号
curl -X POST http://127.0.0.1:3588/api/checkin -d '{"account_id":"acct_xxx"}'
```

**开启每日自动签到**（默认关闭）：控制台勾选「每日 10:00 自动签到」即可，配置存于 `settings.json` 的 `auto_checkin` 字段。
签到时间点可在控制台「自动签到时间」输入框配置（逗号分隔 `HH:MM`，留空为 `10:00`），
或直接写入 `settings.json` 的 `auto_checkin_times` 字段，例如：

```json
{ "auto_checkin": true, "auto_checkin_times": ["09:15", "17:00"] }
```

⚠️ 自动签到依赖服务常驻运行（电脑开机 + qoder2api 启动）。

---

## 更新记录

### 🐛 修复：`--instance=global` 的 Bridge 端口被 settings 默认值冲成 8963

**2026-10-16 14:25 CST** · 由社区贡献者 [@SDCOM-0415](https://github.com/SDCOM-0415) 定位

**问题现象**

按 README 使用 `./qoder2api --instance global` 启动，文档承诺 Bridge 端口为 `8964`，
实际却监听 `8963`；`ss -tlnp | grep 8964` 无结果，按 8964 接入的客户端全部连不上。

**根因**

`ensureConsolePassword()` 在**首次启动**时会 `LoadSettings()`（无 `settings.json` 时返回默认
`Port=8963`），把 `{port:8963, console_password:...}` **写盘**，并同步
`svc.bridgePort = cur.Port`。这一步发生在 `--instance` 预设端口应用**之后**，
于是 `--instance=global` 预设的 `8964` 被静默覆盖回 `8963`，且无任何端口相关日志。

> 注：并非 `LoadRuntimeSettings()` 的问题——`main.go` 在其后已用 `*bridgePort` 修正过，
> 真正冲掉的是随后执行的 `ensureConsolePassword()`。

**修复**

- `auth.go`：`ensureConsolePassword()` 只同步 `BridgeToken`，**不再覆盖** `svc.bridgePort`
- `main.go`：把端口最终裁决移到 `ensureConsolePassword()` **之后**，并明确优先级：
  `显式 --instance 预设 > 显式端口 flag > settings.json > 内置默认`

**验证（全新数据目录）**

| 启动参数 | 修复前 | 修复后 | 说明 |
|---|---|---|---|
| `--instance=global` | ❌ `8963` | ✅ `8964` | 本 Bug 主场景 |
| `--instance=cn` | `8963` | ✅ `8963` | 回归 |
| `--bridge-port=9999` | `9999` | ✅ `9999` | 显式 flag 优先（`9999` 仅为测试用任意端口） |
| 无端口参数（默认 cn） | `8963` | ✅ `8963` | 回归 |

> 表中 `9999` 是验证"显式 `--bridge-port` 是否优先生效"所用的**任意空闲测试端口**，
> 并非项目默认或推荐端口。生产请使用默认 `8963`（CN）/ `8964`（Global）。

---

### 📌 签到设备指纹政策（Global 需真实官方指纹）

**2026-10-16 CST** · 关联 [issue #4](https://github.com/Zhengyuuuui/qoder2api/issues/4) · 对照 [yetone/magpie#1268](https://github.com/yetone/magpie/issues/1268)

国际版账号签到恒为 `no_campaign` 的根因与处理方式：

- **根因**：`campaigns` 接口需要**真实**的 `cosy-machinetoken` + `cosy-machinetype`
  （官方客户端本地持有），缺失时服务端不下发 `CLAIM_BEFEFIT` 活动。
  A/B 实验：补入后活动出现，移除任一字段活动消失
- **自造指纹无效**：`cosy-machinetoken` 由客户端本地生成且受服务端校验，
  任意自造值（含正确长度/格式）都会被拒 → 此前 `7702e28` 移除自造指纹方向正确但不完整
- **指纹不是登录下发的**：实测 `deviceToken/poll`、`deviceToken/refresh` 响应中
  均无这些字段，需从客户端本地或抓包获取
- **懒下发**：Global 账号在官方客户端首次领取后，之后不带指纹也能查到活动——
  解释了「同一控制台内有的 Global 号正常、有的恒 no_campaign」
- **实现**：新增账号级 `checkin_fingerprint` 配置（`account/model.go`），
  `checkinHeaders` 有指纹才附带、无指纹只发必要头（CN 行为）
- **工具**：新增 `scripts/extract_fingerprint.sh`（读取明文 `machine_id` 并给出抓包指引）；
  `no_campaign` 时日志打印指纹状态、活动类型与原始响应，便于排查

### 🏗️ 架构：单控制台 + 双桥端口（CN 8963 / Global 8964）

**2026-10-10 21:20 CST** · [`6199925`](https://github.com/Zhengyuuuui/qoder2api/commit/6199925)

Global Web 控制台（3589）闲置但其 Bridge 链路仍需保留，故将两个 Bridge 合并到同一控制台实例：

- 新增 `--global-port`（默认 `8964`，传 `0` 关闭）
- `--instance=cn` 单进程启动 `console 3588 + bridge 8963 + bridge 8964`
- `8964` 使用 Global 专用账号池；`8963` 保留全量池并按模型 region 路由（见下方跨区修复）
- `--instance=global` 保留为独立兜底模式（`3589 + 8964`）

---

### 🐛 修复：请求跨区域路由导致误扣 Global 账号额度

**2026-10-12 21:31 CST** · [`f4b22a0`](https://github.com/Zhengyuuuui/qoder2api/commit/f4b22a0)

**问题现象**

单控制台 + 双桥改造后，CN 桥（8963）把请求派给了 **Global 账号**并扣除其 Credits。
实测 `yusheng zheng`（国际版）在客户端未启动的情况下被消耗 549 Credits。

**根因**

8963 使用 `buildAccountPool()` 构建账号池，**不区分 region**，CN 与 Global 账号混在一起。
选号逻辑 `pickSlotForModel` 只检查模型的**目录归属**，从不检查账号的 **region**：

```go
// ❌ 修复前
slot, ok := b.pool.Pick(exclude, func(s *Slot) bool {
    return b.slotSupportsModel(s, model)   // 只看模型目录，不看 region
})
if ok { return slot, true }
return b.pool.Pick(exclude)               // 兜底完全不看 region
```

由于 `qmodel_38max` / `qfmodel` 等模型在**两个区域的目录中都存在**，
CN 请求因此会被匹配到 Global 账号并扣费。

**修复内容**

| 文件 | 修改 |
|------|------|
| `service.go` | 8963 改用 `buildRegionPool(acct.Region)`，池内只含同区域账号；8964 保持 Global 专用池；删除已无调用者的 `buildAccountPool()` |
| `internal/bridge/bridge_pool.go` | 新增 `NewPoolBridgeFor(region, pool, tmpl)` 将区域绑定到 Bridge；选号的**目录匹配**与**兜底**均增加 region 过滤，杜绝跨区选号 |
| `internal/bridge/bridge.go` | 日志新增账号名 / ID / region / 模型 |

**端口与账号的隔离关系**（修复后）：

```
console 3588    管理全部账号
bridge  8963    region=cn      仅使用 region=cn 的账号
bridge  8964    region=global  仅使用 region=global 的账号
```

**排查用日志**

每次调用上游都会记录实际使用的账号，便于核对扣费归属：

```bash
# 查看某次请求实际用了哪个账号
grep "using account" ~/.qoder2api-cn/logs/qccg-*.log

# 排查是否有跨区误用（应只出现在 8964 侧）
grep "using account" ~/.qoder2api-cn/logs/qccg-*.log | grep region=global
```

输出示例：

```
callQoder region=cn model=gmodel prompt=hi
callQoder: using account=nick8034851628 id=acct1789... region=cn model=gmodel
callQoder: account=yusheng zheng id=zysgm... region=global failed, failing over to next account: ...
```

### 签到：支持 Global 国际版 + 修复「显示签到但没到账」

**2026-10-10 ~ 2026-10-11** · [`b84c41b`](https://github.com/Zhengyuuuui/qoder2api/commit/b84c41b) 双区域 · [`fcde08e`](https://github.com/Zhengyuuuui/qoder2api/commit/fcde08e) 可配置时间 · [`2cd2ccd`](https://github.com/Zhengyuuuui/qoder2api/commit/2cd2ccd) 设备指纹

> 本节特性由社区贡献者 [@LeonardW-sl](https://github.com/LeonardW-sl) 主导实现，详见文首[致谢](#🙏-特别致谢--贡献者)。

- **双区域签到**（`b84c41b`）：`account.Endpoints` 新增 `OpenAPIHost`，签到域名按账号 `region` 自动路由
  （`cn` → `openapi.qoder.com.cn`，`global` → `openapi.qoder.sh`），
  同一控制台实例可同时管理国内版与国际版账号，无需分实例
- **修复国际版签到不可用（重要）**：抓包对比官方客户端发现，国际版签到活动**按设备指纹下发**。
  此前签到请求只带 `Authorization` + `cosy-clienttype`，缺少
  `cosy-version` / `cosy-machineid` / `cosy-machinetoken` / `cosy-machinetype` /
  `cosy-machinecode` / `cosy-machinehostname` / `cosy-machineos` 共 7 个头，
  服务端因此不下发 `CLAIM_BENEFIT` 活动，表现为「无可用签到活动」。
  现已复用 Bridge 的 `cosy.FingerprintSeed` 派生规则补齐全部指纹头，
  国际版账号签到恢复正常（实测由 `no_campaign` 变为可领取并成功到账）
- **修复空签到（重要）**：`daily-check-in/claim` 在国内已 `DISABLED`，却对**未领取日也恒返回 409**
  （被误读为「已领取」而跳过真实领取）；海外该端点直接 404。
  现已**彻底移除**对它的领取调用，`campaigns` 成为唯一权威签到路径（返回 `grantId` 可核验到账）
- **窗口日期记账**：本地签到历史改按活动窗口 `startAt` 记录，而非自然日。
  此前在 `10:00` 前点击（仍属前一日窗口）会把当天误记为已签，连续天数虚高
- **提示更明确**：未到新窗口时明确输出「已领取 YYYY-MM-DD 窗口额度（新窗口 MM-DD 10:00 开放）」
- **调度防漏签**：10:00 整点若活动尚未下发，不再标记当日完成，改为每分钟重试至 12:00
- **连续签到统计**：本地 `checkin_history.json` 按账号分别记录，
  跨天自动累计「连续 N 天 / 累计 N 天 / 共 N 积分」，支持断签识别
- **签到时间可配置**（`fcde08e`）：`auto_checkin_times` 支持多时段（HH:MM 列表），留空默认 `10:00`

### 流稳定性 / 错误分类 / 安全加固

**2026-10-07 20:56 CST** · [`be38b8f`](https://github.com/Zhengyuuuui/qoder2api/commit/be38b8f)（合并 `LeonardW-sl` 多账号池 / 图片输入 / 区域路由）

**上游流处理（对齐 hub 行为）**

- **错误帧及时中止**：上游 HTTP200 建流后在信封里投递错误帧（418/5xx 等）且不关流时，
  网关立即停止读取并按瞬时故障自动重开上游（有界重试，对调用方无感），请求不再挂起
- **空流显式报错**：上游建流成功但零有效帧即关流时返回 `empty upstream stream`；
  三协议流式路径输出 error 事件帧（chat 为 data 错误帧，claude/codex 为 `event: error`），
  非流式路径返回 500 + 错误 JSON（此前为空内容正常 finish，属与 hub 对齐的有意变更）
- **错误四分类**：上游错误按「内容审核 / 瞬时可重试 / 客户端参数 / 普通上游」分类，
  对客户端输出友好中文消息与正确 HTTP 状态（内容审核 400、瞬时耗尽 502 等）；
  同账号瞬时故障（418/5xx/传输抖动）连接层 1s/2s 退避自动重试
- **usage 同帧合并**：上游同帧返回 usage + choices 时，token 用量与内容一并返回，不再二选一

**安全**

- **Bridge 端点鉴权**：`/v1/*` 必须携带 API Key（默认 `qccg`，控制台可修改）——
  支持 `Authorization: Bearer <token>` 或 `x-api-key: <token>`，常量时间比较；
  OPTIONS 预检直接 204 放行；校验失败返回 401 + OpenAI 风格错误体。
  同网络无凭证客户端不再能消耗账号配额
- **短 token 防护**：误粘贴短 token 不再导致启动 panic，日志只输出前缀
- **PII 降级**：userinfo 原始响应日志降为 Debug 级并截断 500 字节，默认日志级别不再落盘
- **未跟踪文件隔离**：`.gitignore` 新增 `.ydevsphere/`、`webconsole/*.bak*`、`scripts/capture/`
  （抓包流量目录，可能含凭证，仅存本地、不入库，请自行处理）

**设备指纹与可观测性**

- **本机指纹盐**：`settings.json` 的 `machine_salt` 首次启动自动生成，使每个部署的
  指纹派生空间独立（uid + 本机盐派生机器码，重启不变）；**勿手动修改，变更即全部账号指纹漂移**
- **盐降级明示**：盐加载失败时日志明确写出「运行于无盐模式（hub 兼容指纹），下次启动
  恢复加盐将导致全部账号指纹漂移」；成功输出 `machine salt loaded (len=N)`；
  `cmd/checkin` 调试工具失败时向 stderr 告警（不阻断运行）

**测试与工具**

- `internal/bridge`、`internal/cosy` 新增单元/回归测试（信封重试闸门、错误帧停读信号、
  空流报错、usage 合并、指纹派生等），离线可跑、无外部依赖
- 新增 `cmd/checkin` 调试工具（只读查询活动/额度接口）与 `scripts/` 签到辅助脚本
  （`checkin.py` 等，凭证运行时从本地数据目录读取，无硬编码）

---

## 功能

- 🎁 **每日签到**：一键/单账号/定时自动领取每日 100 Credits（见上）
- 将 Qoder 账号转为本地兼容 API，供 **NewAPI**、**OpenCode**、**Claude Code**、**Codex** 等客户端使用
- Bridge 端点鉴权：`/v1/*` 需携带 API Key（默认 `qccg`，控制台可改）
- 多账号管理：支持 **OAuth** 与 **PAT**
- 账号额度展示（套餐额度 + 个人拓展包分开显示）
- 模型列表展示上下文窗口 / 最大输出 / 推理支持
- 一键复制 NewAPI 渠道配置（Base URL + API Key）
- 数据与密钥落盘，适合服务器常驻与保活

## 端口

**默认（`--instance=cn`，推荐）**：单进程同时提供控制台与两个 Bridge

| 服务 | 端口 | 说明 |
|------|------|------|
| Web 控制台 | `3588` | **唯一控制台**，管理全部账号（CN + Global） |
| Bridge（CN） | `8963` | 仅使用 `region=cn` 账号，按模型 region 路由 |
| Bridge（Global） | `8964` | 仅使用 `region=global` 账号，可通过 `--global-port=0` 关闭 |

数据目录：`~/.qoder2api-cn`（账号集中存放，按 `region` 字段区分归属）

客户端接入：

- CN 用户 → `http://<host>:8963/v1`
- Global 用户 → `http://<host>:8964/v1`

<details>
<summary>兼容模式：独立 Global 实例（通常不需要）</summary>

| 实例 | Web 控制台 | Bridge API | 数据目录 |
|------|------------|------------|----------|
| `--instance=cn` | `3588` | `8963` + `8964` | `~/.qoder2api-cn` |
| `--instance=global` | `3589` | `8964` | `~/.qoder2api-global` |

</details>

双开请用 `--instance`：

```bash
# 国内
./qoder2api --instance=cn

# 国际
./qoder2api --instance=global
```

## Docker 部署

### 本地构建运行

需要安装 Docker Engine 与 Docker Compose：

```bash
docker compose up -d --build
```

控制台位于 `http://localhost:3588`，Bridge 端口为 `8963`（CN）和 `8964`（Global）。账号、密钥与设置保存在 Compose 命名卷 `qoder2api-data` 中；可通过环境变量 `QODER2API_CONSOLE_PASSWORD` 指定控制台密码，留空则首次启动时自动生成并保存。

查看运行日志或停止服务：

```bash
docker compose logs -f
docker compose down
```

### 点击按钮构建并推送到 GHCR

在 GitHub 仓库打开 **Actions → Build and publish Docker image → Run workflow**，输入要发布的镜像标签并运行。工作流会构建 `linux/amd64` 和 `linux/arm64`，推送输入标签以及对应的 `sha-<commit>` 标签到：

```text
ghcr.io/<仓库所有者>/qoder2api:<tag>
```

镜像命名空间由工作流的 `GITHUB_REPOSITORY` 自动推导（例如本仓库发布到 `ghcr.io/zhengyuuuui/qoder2api`），fork 后无需改动即可发布到自己账号下。工作流使用仓库的 `GITHUB_TOKEN`，无需额外配置推送密钥。首次发布后，可在仓库 **Packages** 中调整镜像可见性；拉取公开镜像示例：

```bash
docker pull ghcr.io/zhengyuuuui/qoder2api:latest
```

## 快速开始

### 1. 编译

```bash
git clone <你的仓库地址>/qoder2api.git
cd qoder2api
go build -ldflags="-s -w" -o qoder2api .
```

Linux 交叉编译示例：

```bash
GOOS=linux GOARCH=amd64 go build -ldflags="-s -w" -o qoder2api .
```

### 2. 启动（控制台 + 桥一体）

一条命令同时启动 **网页控制台** 与 **Bridge 服务框架**：

```bash
./qoder2api --web-port=3588 --bridge-port=8963
```

可选参数：

| 参数 | 默认 | 说明 |
|------|------|------|
| `--web-port` | `3588` | Web 控制台端口 |
| `--bridge-port` | `8963` | Bridge API 端口 |
| `--bind` | `0.0.0.0` | 控制台监听地址 |
| `--data-dir` | `~/.qoder2api` | 数据根目录（账号/密钥隔离） |
| `--instance` | 空 | `cn`→3588 + 8963 + 8964；`global`→3589 + 8964（独立模式） |
| `--global-port` | `8964` | Global 专用桥端口；`0` = 不启动 |
| 环境变量 `QODER2API_HOME` | — | 等同 `--data-dir` |
| 环境变量 `QODER2API_CONSOLE_PASSWORD` | — | 控制台登录密码 |

启动成功后日志类似：

```text
qoder2api console: http://0.0.0.0:3588
bridge target port: 8963
```

### 3. 打开网页控制台（需登录）

浏览器访问：

```text
http://127.0.0.1:3588
```

会跳转到 `/login.html`。控制台密码来源（优先级从高到低）：

1. 环境变量 `QODER2API_CONSOLE_PASSWORD`
2. `~/.qoder2api/settings.json` 里的 `console_password`
3. 若都没有，**首次启动自动生成**并写入 settings，日志会打印一次

```bash
export QODER2API_CONSOLE_PASSWORD='你的强密码'
./qoder2api --web-port=3588 --bridge-port=8963
```

服务器部署时替换为：

```text
http://<服务器IP>:3588
```

登录后在控制台中：

1. 选择 Region（`global` / `cn`）
2. 点击 **OAuth 登录**（或填写 PAT 添加）
3. 授权成功后账号会写入本地，并自动 **激活**
4. 激活后 **Bridge 自动监听 8963**

> 说明：控制台密码只保护 **管理页**；Bridge 仍用独立 API Key（默认 `qccg`），与 NewAPI 对接不受影响。  
> CN / Global 双开时，会话 cookie 按端口隔离（`qoder2api_session_3588` / `qoder2api_session_3589`），同一浏览器可同时登录两端。

### 4. 启动 / 确认 Bridge

- **自动**：激活账号后会自动 `startBridge`
- **手动**：控制台可调用启动逻辑；也可用 API：

```bash
# 查看状态
curl http://127.0.0.1:3588/api/status

# 确认 Bridge 模型列表（需携带 API Key，默认 qccg）
curl -H "x-api-key: qccg" http://127.0.0.1:8963/v1/models
```

若返回模型 JSON，说明桥已正常；返回 401 说明 API Key 缺失或不匹配。

---

## 使用方法

### Bridge 端点

| 端点 | 兼容格式 |
|------|----------|
| `POST /v1/chat/completions` | OpenAI Chat |
| `POST /v1/messages` | Anthropic Claude |
| `GET  /v1/models` | 模型列表 |
| `POST /v1/responses` | OpenAI Responses (Codex) |

### 默认接入信息

| 字段 | 值 |
|------|-----|
| Base URL (OpenAI) | `http://127.0.0.1:8963/v1` |
| API Key | `qccg`（可在控制台修改） |
| 推荐模型 | `Auto` 或实时列表中的 `Kimi-K3` |

控制台 **「NewAPI / 中转站接入」** 区域可复制完整配置。

### 接入 NewAPI

1. 打开 qoder2api 控制台 → 复制 Base URL 与 API Key  
2. NewAPI 新建渠道：
   - 类型：`OpenAI`
   - Base URL：`http://<host>:8963/v1`
   - Key：控制台中的密钥  
3. 从 `GET /v1/models` 复制 `id`，例如 `Auto`、`Kimi-K3`；控制台生成的配置也使用这些名称，不填写 `kmodel_latest` 等内部 key。

### 接入 OpenCode

在 `~/.config/opencode/opencode.json` 中增加 provider：

```json
{
  "$schema": "https://opencode.ai/config.json",
  "provider": {
    "qoder2api": {
      "npm": "@ai-sdk/openai-compatible",
      "name": "Qoder2API",
      "options": {
        "baseURL": "http://127.0.0.1:8963/v1",
        "apiKey": "qccg"
      },
      "models": {
        "Auto": { "name": "Auto" },
        "Kimi-K3": { "name": "Kimi-K3" }
      }
    }
  },
  "model": "qoder2api/Kimi-K3"
}
```

### 接入 Claude Code

```bash
# 示例：环境变量方式（也可写 ~/.claude/settings.json）
export ANTHROPIC_BASE_URL="http://127.0.0.1:8963"
export ANTHROPIC_AUTH_TOKEN="qccg"
export ANTHROPIC_MODEL="Kimi-K3"
claude
```

### 命令行快速自测

```bash
curl http://127.0.0.1:8963/v1/chat/completions \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer qccg" \
  -d '{"model":"Kimi-K3","reasoning_effort":"max","max_completion_tokens":65536,"messages":[{"role":"user","content":"你好"}]}'
```

Responses 示例（无状态，不保存历史；示例调用会产生真实推理费用）：

```bash
curl http://127.0.0.1:8963/v1/responses \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer $QODER2API_SK" \
  -d '{"model":"Kimi-K3","input":"你好","store":false,"stream":true,"reasoning":{"effort":"max"},"max_output_tokens":65536}'
```

---

## 数据目录

```text
~/.qoder2api/
├── accounts/          # 账号元数据 JSON
├── secrets/           # OAuth/PAT 凭证（权限 0600）
├── settings.json      # 端口、bridge_token 等
└── logs/              # 运行日志
```

服务器上请保证进程用户对 `$HOME/.qoder2api` 可写；用 Docker 时挂载数据卷到 `/data`（`HOME=/data`）。

---

## Docker 部署

```bash
docker build -t qoder2api .
docker run -d --name qoder2api \
  -p 3588:3588 -p 8963:8963 \
  -v qoder2api-data:/data \
  -e HOME=/data \
  qoder2api
```

然后访问：`http://<服务器IP>:3588` 完成 OAuth 登录。

## systemd 部署

```bash
sudo mkdir -p /opt/qoder2api
sudo cp qoder2api /opt/qoder2api/
sudo cp qoder2api.service /etc/systemd/system/
sudo systemctl daemon-reload
sudo systemctl enable --now qoder2api
```

---

## 项目结构

```text
qoder2api/
├── main.go              # 入口：Web + API
├── service.go           # 账号 / Bridge 业务
├── checkin.go           # 每日签到（手动 API + 10:00 自动调度 + 连续天数统计 + CN/Global 双区域）
├── account/             # 账号、OAuth、设置、密钥文件存储
├── cmd/checkin/         # 签到/额度只读调试工具
├── internal/
│   ├── bridge/          # OpenAI / Claude / Codex 兼容层 + 错误分类
│   └── cosy/            # Qoder 签名、会话与设备指纹
├── logger/
├── scripts/             # 签到辅助脚本与抓包工具
├── webconsole/          # 轻量 HTML 控制台
├── baseprompt.json
├── Dockerfile
└── qoder2api.service
```

---

## 致谢

- **[QCCG](https://github.com/wangtufly/QCCG)**（[wangtufly](https://github.com/wangtufly)）  
  本项目在 QCCG 之上进行二次开发：复用 / 精简了 Bridge、账号体系、OAuth 与 Qoder 协议相关实现，并改为无 GUI 的服务器友好形态。  
  **感谢原作者的开源工作。** 若你需要完整的 macOS 桌面端体验，请优先使用官方 QCCG。

- 上游思路与生态亦受益于 Qoder 社区及相关逆向/兼容项目（见 QCCG 仓库 README 中的鸣谢）。

---

## 免责声明

本项目仅供学习与自用。请遵守 [Qoder](https://qoder.ai) 服务条款与当地法律法规；账号与 API 使用风险自负。

---

## License

二次开发基于 [QCCG](https://github.com/wangtufly/QCCG) 的开源协议；请同时遵守原项目 [LICENSE](https://github.com/wangtufly/QCCG/blob/main/LICENSE) 的要求。
