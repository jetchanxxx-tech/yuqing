# beta 0.2.3 部署报告（2026-09-29）

## 状态

已部署本轮候选源码 `81e69c5` 的 Go server/worker/CLI、前端及 query/report 引擎增量，并应用 `da0d10f` 的接口版本修复和只读运维检查脚本；生产源码包已同步该增量，正式 PostgreSQL goose 从 7 升至 12。GitHub 草稿 PR #5 最新提交的 PostgreSQL Store Tests 检查成功。此为用户授权的提前部署，不代表 PRD 全量功能验收通过或草稿 PR 已合并。

## 数据操作与备份

- 按用户确认，删除指定的 9 条历史自动化测试分析及其 98 条原文；未按名称或租户批量删除其他分析，8 条其他分析保留。
- 关联的 16 条额度流水全部保留，未修改余额、未执行退款；首次保护性事务遇到流水引用时已回滚，核对后仅删除分析和原文。
- 变更前应用/配置/数据库备份：`/root/yuqing-backups/beta023-upgrade-20260929T060443Z`。数据库 dump 列表和上传包哈希已校验；此次按用户要求未重复演练。
- 正式迁移执行 0008–0012，未手工修改 goose 版本。已检查 goose 12、剩余创建人无缺失；server/worker 均使用 PostgreSQL 持久队列，显式设置 `YUQING_BETA_SKIP_CREDITS=true`。

## 实际验证

- 七项 systemd 服务 active，API 与五个 Python 引擎健康检查成功；query/report 重启后的短暂 readiness 失败随后恢复并重新检查通过。
- 公网站点主页 200，匿名新版模板接口 401（不是旧版 404）；真实 API 临时测试账号完成注册、登录、读取六模板和草稿列表。
- 现网 API 创建测试分析 `01M3NWEW02SG2XPPWWZN43ZB0S`，独立 worker 将其由 queued 推至 fetching，随后 API 取消成功。这是 PostgreSQL/队列投递验收，不是完整真实来源分析或报告验收。该新测试账号/任务保留并明确标记为部署 smoke fixture。
- 本轮未测试扣费、支付或短信，遵循用户要求；未运行完整浏览器 E2E。
- 最终公网健康接口返回 `{"status":"ok","version":"0.2.3-beta"}`；现场运行 `scripts/post-upgrade-check.sh` 成功。最终 server SHA256 为 `d05c8494e2e5ea8cbe1b0d012383a8e2ca6893ee9e9eec547f1aed645beeea28`，worker 为 `38a5b44e344f229b681373e0c93c63f12668d5eb7fae90f3161abc7014b12c81`，CLI 为 `dcd90c612bbc11f3c98cea0eb0f581b708acd3db7c375e013b0c9a75f9944274`，前端 index 为 `7209808361a84d005445007a81373da7378c756ed6ff012cd59f14c196065a59`。

## 未完成与回滚

方案实际运行入口、周期调度、来源能力闭环和非短信通知仍未完整实现；保持 UI 的不可用提示，不宣称全量 beta 发布通过。接口旧版本标识单独修正为 `0.2.3-beta`，以实时健康检查和最终产物哈希为准。

回滚须先停止 server/worker 写入，保全升级后数据，评估新旧 schema 兼容性；原应用、配置和前端在上述备份目录。数据库全量恢复会覆盖升级后数据，不能无条件自动恢复；不得执行 goose Down 来猜测恢复历史 hotfix 状态。

后续只读运行核验使用 `bash scripts/post-upgrade-check.sh`；该脚本不执行删除、迁移、部署或完整业务验收。

## 2026-09-29 洞察超时后续修复

- 现场任务 `01M3NX9STYDCEVV2STRJT85593` 在采集 37 篇文档后，worker 等待 insight `/analyze` 约 420 秒并记录 `Client.Timeout exceeded while awaiting headers`；任务随后带告警完成。Python 日志在超时前记录热度维度的无效 JSON 和重试。
- 已将独立的情感/话题与维度调用放入共享的三路并发闸门，并为单次 `/analyze` 设置 390 秒总预算（Go HTTP 客户端为 420 秒）。预算到期时取消未完成维度，保留已有结果并在 `warning` 说明；模型读取超时也显示明确原因。仅更新 `/opt/pangu-source/engines/insight_engine/main.py` 并重启 `yuqing-insight`；最终文件 SHA256 为 `73bf223a4add2c6eb8129a96935c7509d29a61512586bb3e017e9ef60536b9b9`。修复前版本和首轮修复版本分别备份在 `/root/yuqing-backups/insight-timeout-20260929T100525Z/` 与 `/root/yuqing-backups/insight-timeout-20260929T103910Z/`。
- 本地 Python 引擎测试 **110 passed**；生产 `py_compile`、服务 active、8002 `/health` 和最终文件哈希已核对。一次合成短文档请求**直接调用 8002，未携带平台后台的 LLM URL/模型/Key**，因此退回引擎环境默认的 `sub.geiliapi.com`；该请求在 8002 日志中返回 HTTP 200，但上游多次返回 502，洞察因此降级。这只能说明引擎默认路径的结果，不能用于判断平台分析任务访问了哪个提供商，也不证明完整真实分析通过。
- browser-act 的 `chrome-direct` 在当前 WSL 环境找不到 Windows Chrome 用户目录，未打开页面。用户决定暂不切换隔离 Chrome，故浏览器验收未执行。截至这一步尚未新建平台分析任务，也尚未验证本修复后真实来源任务的完整洞察与报告。

### LLM 端点归因更正

- 生产 `platform_settings` 中的 `llm_base_url` 是 `https://open.bigmodel.cn/api/coding/paas/v4`，`llm_model` 是 `GLM-5.3-Flash`，Key 非空；这两项记录的更新时间早于上面发生 420 秒超时的任务。worker 文件哈希仍是本报告所列版本，源码在每次任务发送给 insight 前读取平台设置并透传。
- 生产 `engines.env` 另有 `LLM_BASE_URL=https://sub.geiliapi.com/v1` 和 `LLM_MODEL=deepseek-v4.1-flash`；只有请求没有提供平台 URL/模型时，Python 引擎才使用这组默认值。此前把直接调用 8002 的日志当成平台任务端点，是错误归因。
- 用平台数据库中的智谱 URL、模型和 Key 做了两次短请求探测：普通 chat 约 3.1 秒成功，带 JSON 与 quick thinking 参数的调用约 2.2 秒成功。短请求成功不代表 37 篇文档、多维度和摘要的整次分析能在 420 秒内完成；原始任务请求体未留审计记录，不能仅凭这些探测倒推出其历史网络目标。
- 显式携带平台智谱配置直接调用 8002 `/analyze`：一篇合成短文档的 quick 模式在 160.8 秒返回 HTTP 200，3 个维度、情感和摘要有结果，告警为空；此项没有经过 Go worker。
- 只读提取原任务的 37 篇已存文档，在服务器内以 quick 模式和平台智谱配置直接重放到 8002；服务日志显示深层原因维度无效 JSON 后重试、摘要生成超时，最终 `/analyze` 返回 HTTP 200。SSH 汇总输出未返回，故无法确认其他维度和情感结果的数量，也无法获得可靠的请求总耗时。未创建或重跑平台分析任务，此结果不等于 Go 管线或浏览器 E2E 验收通过。

## 修复后生产 Go 管线复测

- 使用 `admin@pangu.com` 的生产 API 会话，新建标记为诊断复测的任务 `01M3QEMJ18XJ5XWHZEAV23G750`，复用原任务的 2 个关键词和 3 个来源；未重跑或改写原任务。
- 新任务采集 37 篇文档，约 551 秒后状态为 `completed`，结果接口 HTTP 200，有报告；`warning` 不再包含 `Client.Timeout exceeded`。这证实本次复测中 Go 等待 8002 的原报错未再发生。
- 洞察只有 1 个维度，摘要为空；`warning` 标记部分维度失败及摘要超时。因此不能将 `completed` 等同于完整研判验收通过。诊断任务作为明确标记的生产测试记录保留，未删除。
- 提交前重新运行 Python 引擎测试 `110 passed` 和 Go 全套 `go.exe test ./... -count=1`（退出码 0）；PostgreSQL 合同测试若缺少专用 `YUQING_TEST_PG_URL` 会跳过，不能据此宣称数据库集成全测。
