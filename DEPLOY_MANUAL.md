# 手动部署指南 - created_by列修复

> 归档：此手册的数据库名和补列脚本与当前生产结构不一致，不得直接执行。请先按 `docs/ops/DEPLOYMENT_RUNBOOK.md` 核对当前数据库和迁移状态。

## 前置条件
- 本地已有 `HOTFIX_0008_created_by.sql` 文件
- SSH密码：见 credentials.local.md（勿写入仓库）

## 执行步骤（复制粘贴到PowerShell）

### 步骤1: 上传SQL文件
```powershell
scp -P 22352 HOTFIX_0008_created_by.sql jet@101.96.209.90:/tmp/
```
输入密码：`<见 credentials.local.md>`

### 步骤2: SSH登录服务器
```powershell
ssh -p 22352 jet@101.96.209.90
```
输入密码：`<见 credentials.local.md>`

### 步骤3: 在服务器上执行（登录后复制粘贴）

```bash
# 检查PostgreSQL状态
sudo systemctl status postgresql

# 执行修复SQL
sudo -u postgres psql -d yuqing_db -f /tmp/HOTFIX_0008_created_by.sql

# 验证修复结果
sudo -u postgres psql -d yuqing_db -c "
SELECT column_name, data_type, is_nullable, column_default
FROM information_schema.columns
WHERE table_name = 'analyses' AND column_name = 'created_by';
"

# 重启Go服务
sudo systemctl restart yuqing-server

# 等待3秒
sleep 3

# 检查服务状态
sudo systemctl status yuqing-server

# 验证API
curl -s http://localhost:8080/health | jq .

# 清理临时文件
rm /tmp/HOTFIX_0008_created_by.sql

echo ""
echo "✅ 部署完成！"
```

## 预期结果

1. SQL执行输出：
```
ALTER TABLE
ALTER TABLE
ALTER TABLE
```

2. 列验证输出：
```
 column_name | data_type | is_nullable | column_default 
-------------+-----------+-------------+----------------
 created_by  | bigint    | NO          | 1
```

3. 服务状态：`active (running)`

4. API响应：
```json
{
  "status": "ok",
  "timestamp": "..."
}
```

## 如果出错

- SQL执行失败：检查PostgreSQL服务是否运行
- 服务启动失败：`sudo journalctl -u yuqing-server -n 50` 查看日志
- API无响应：检查端口 `sudo netstat -tlnp | grep 8080`
