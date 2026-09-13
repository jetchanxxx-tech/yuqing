-- +goose Up
-- 本文件补齐 pgx store 需要、而 0001_init.sql 尚未提供的两处结构。
-- 全部语句幂等（IF NOT EXISTS），重复执行与「重跑部署脚本」都安全。
--
-- 背景（源自实现 pgx store 时的走查）：
--   1. settings.Store 需要一张平台级 KV 表；0001 里没有，缺表则
--      「管理后台 → 数据源配置」的在线配置无处落库。
--   2. api_keys 没有存显示前缀的列（APIKey.Prefix 只存在于内存里），
--      读回来是空串，后台列表就显示不出密钥前缀。
--
-- platform_settings 表已由 0002_tenant_data.sql 创建，本文件只补 api_keys.prefix。

-- 显示前缀（pangu_ + 原始 key 的前 6 位），不是秘密，仅用于后台区分不同密钥。
-- 默认空串：历史行读出来是空前缀而不是 NULL，前端不必处理 null。
ALTER TABLE api_keys ADD COLUMN IF NOT EXISTS prefix TEXT NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE api_keys DROP COLUMN IF EXISTS prefix;
