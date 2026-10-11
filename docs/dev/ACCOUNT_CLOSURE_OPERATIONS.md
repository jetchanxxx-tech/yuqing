# 注销回收运维说明

本功能在开发分支通过 GitHub 托管验收，生产尚未升级。发布时按 AGENTS.md 选用 prod 目标提交的成功构建，下载预构建制品并核对 archive.sha256、BUILD_COMMIT 和解包 SHA256SUMS；保留回滚制品。

服务与 CLI 必须使用同一个持久私有头像卷及配置。配置中的相对 storage.avatarRoot 以配置文件目录为基准；建议使用绝对路径。申请时记录持久卷身份，后续身份缺失或不符会保留账号身份并报等待错误；不能通过换用空目录宣称回收完成。

使用预构建 CLI，通过 YUQING_CONFIG 指定配置：

```text
account-closure preview --user-id 已核对的不可变用户ID
account-closure execute --user-id 已核对的不可变用户ID --limit 100
account-closure retry --user-id 已核对的不可变用户ID --limit 100
```

命令执行有界步骤，成功退出也可能返回等待／受阻／保留期未到。读取 state、cleanup_status、error_code、next_attempt_at、进度计数和 cleanup_completed_at 后再判断结果。limit 是各阶段批次上限，不是强制删除或绕过业务检查。

预构建服务每 20 秒发现到期／重试及已注销账号的保留期回收，每轮最多 5 个账号、各阶段 100 个对象，每账号上下文 30 秒。持久 next_attempt_at 支持重启恢复；PG 每账号会话锁使重叠的服务与 CLI 执行安全串行。

- pending：严格 168 小时撤回期限之前可撤回；到期后重新检查当前订单、退款、发票、任务和通知终态。
- finalizing：不能撤回，按记录的阶段和错误重试。单人／多人团队范围变化需要重新核对，不删除其他成员资产。
- completed：在线账号标识已脱敏，财务及账务关联保留；业务 cleanup_status 仍可能为 retention_pending 或 pending_cleanup。
- cleanup_status 为 complete：已支持的在线回收阶段完成，不能据此声称备份、供应商副本或历史财务事实已删除。

业务保留期使用申请时真实目录快照及原业务创建时间。未知策略、外部／财务附件适配器保持受阻状态。不得猜路径删除，也不得将订单、退款或用量改成虚假终态以通过检查。

processing 通知保留尝试、收件信息及回执 CAS，直到原尝试记录实际结果或得到明确核对。租约过期不是发送结束证明；不得人为改成 accepted/cancelled 绕过受阻状态。受理回执不等于物理送达，真实供应商送达仍需另行授权验收。

备份／恢复必须保留 account_closures、account_closure_tenants、用户／租户墓碑、持久卷身份及所有者 seal 标记。恢复实例开放流量前恢复最新墓碑和进度，维持已注销所有者写入屏障，执行符合条件的有界回收并检查复用邮箱对应新的不可变 ID。不得恢复旧活动凭据／密钥或转移固定账号计费豁免。离线备份依既有保留与轮换处理，在线回收不宣称已删所有离线副本。

生产运行与恢复演练未在本轮执行。部署及数据操作按另行授权的生产变更安排进行。
