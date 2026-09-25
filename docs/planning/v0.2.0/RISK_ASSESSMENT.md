# v0.2.0 风险评估

**版本**: v0.2.0  
**作者**: 技术总监  
**日期**: 2026-09-25  
**依赖**: [TECHNICAL_DESIGN.md](./TECHNICAL_DESIGN.md) | [DEVELOPMENT_GUIDE.md](./DEVELOPMENT_GUIDE.md)

---

## 目录

1. [风险概览](#风险概览)
2. [技术风险](#技术风险)
3. [时间风险](#时间风险)
4. [依赖风险](#依赖风险)
5. [运维风险](#运维风险)
6. [业务风险](#业务风险)
7. [降级方案](#降级方案)
8. [风险缓解措施](#风险缓解措施)

---

## 风险概览

| ID | 风险描述 | 影响范围 | 概率 | 影响 | 风险等级 | 责任人 |
|----|---------|---------|------|------|---------|--------|
| R1 | LAC NER 中文分词效果不达预期 | F25 | 中 | 中 | **P1** | 后端 |
| R2 | Strategy Engine LLM 生成质量不稳定 | F23 | 中 | 高 | **P0** | 后端 |
| R3 | 竞品并发采集超额度/超时 | F24 | 高 | 中 | **P1** | 后端 |
| R4 | 检查点定时器延迟导致跟踪不及时 | F23 | 中 | 中 | **P2** | 后端 |
| R5 | 指数计算公式与业务预期偏差 | F25 | 中 | 低 | **P2** | 产品+后端 |
| R6 | 迁移脚本在生产环境执行失败 | 全部 | 低 | 高 | **P1** | 后端 |
| R7 | 前端 ECharts 地图加载失败 | F25 | 低 | 低 | **P3** | 前端 |
| R8 | 工作量估算偏差（超期 50%+） | 全部 | 中 | 高 | **P1** | PM |
| R9 | 三个需求相互阻塞（串行开发） | 全部 | 中 | 中 | **P2** | PM |
| R10 | 生产环境内存不足（LAC 模型加载） | F25 | 低 | 中 | **P2** | 运维 |

**风险等级定义**：
- **P0**：阻塞上线，必须解决
- **P1**：严重影响体验，强烈建议解决
- **P2**：中等影响，可延后处理
- **P3**：轻微影响，可接受

---

## 技术风险

### R1: LAC NER 中文分词效果不达预期

**描述**：  
百度 LAC 在舆情文本（非标准新闻语料）上的城市提取准确率可能低于 80%，导致地理分布数据失真。

**触发条件**：
- 文档包含网络用语、缩写（如"魔都"="上海"）
- LAC 模型版本过旧（2.2.0 发布于 2022 年）
- 城市白名单覆盖不全（遗漏县级市）

**影响**：
- 前端地图展示城市数据偏少（< 5 个城市）
- 用户质疑数据真实性

**缓解措施**：
1. **预验证**（Day 1）：
   ```bash
   # 在开发阶段用真实舆情数据测试 LAC
   python engines/tests/test_ner.py --sample-data=production_docs.json
   # 目标准确率 ≥ 70%（50 条文档，人工标注对比）
   ```

2. **白名单扩充**：
   - 添加网络别称映射（`{"魔都": "上海", "帝都": "北京", ...}`）
   - 扩展到地级市（白名单从 34 个扩展到 300+）

3. **降级方案**：
   - LAC 失败时，退回到正则提取（`re.findall(r'(北京|上海|广州|深圳...)', text)`）
   - 前端显示"数据来源：关键词匹配"提示

**Plan B**：
如果 LAC 准确率 < 50%，放弃 NER，改用**用户自定义地域标签**：
- 分析时用户勾选关注地域（如"华东地区"、"一线城市"）
- 后端按白名单关键词过滤文档
- 工作量减少 1 天，但失去自动提取能力

---

### R2: Strategy Engine LLM 生成质量不稳定

**描述**：  
营销策略生成依赖 GLM-4-flash（或 DeepSeek），输出质量受 Prompt 设计和模型状态影响，可能出现：
- 内容空洞（"建议加强宣传"等套话）
- 格式错误（不符合 Markdown 结构）
- 维度建议缺失（应该 5 个，实际只有 2 个）

**触发条件**：
- 基线舆情数据质量差（文档 < 10 条）
- LLM 供应商限流/不稳定
- Prompt 模板与业务预期不匹配

**影响**：
- 用户认为 AI 生成的方案无价值（影响 F23 核心价值）
- 营销活动创建成功率 < 70%（失败进入 `failed` 状态）

**缓解措施**：
1. **Prompt 工程**（Day 3-4）：
   - 加入 Few-Shot 示例（2 个高质量案例）
   - 约束输出格式（JSON Schema 或显式标记）
   - 测试 10+ 真实舆情场景，迭代 Prompt

2. **质量检测**：
   ```python
   def validate_strategy(content: str, dimensions: List[DimensionAdvice]) -> bool:
       # 长度检查
       if len(content) < 500:
           return False
       # 维度检查
       if len(dimensions) < 3:
           return False
       # 关键词检查
       if "建议" not in content or "预期" not in content:
           return False
       return True
   ```

3. **降级策略**：
   - LLM 失败或质量低 → 返回**基于规则生成的模板化方案**：
     ```
     # 营销方案建议
     
     ## 核心策略
     针对当前{负面情感占比}的舆情态势，建议采取以下措施：
     1. 加强正面内容传播
     2. 优化用户体验
     
     ## 分维度建议
     {基于五维研判结论，套用模板}
     ```
   - 前端显示"系统生成"标签，区分 AI 生成

4. **人工审核**（可选）：
   - Beta 阶段：每个生成的策略发送到 admin 邮箱审核
   - 收集反馈，持续优化 Prompt

**Plan B**：
如果 LLM 生成成功率 < 60%，**暂时移除 AI 生成功能**：
- 营销活动仅保留检查点跟踪能力
- "营销方案建议"改为用户手动填写
- 工作量减少 2 天，但失去 F23 核心亮点

---

### R3: 竞品并发采集超额度/超时

**描述**：  
竞品对比需要并发采集 N 个竞品（N ≤ 10），每个竞品扣 1 次额度。存在两个风险：
1. **额度风险**：用户额度不足以覆盖所有竞品
2. **性能风险**：10 个竞品 × 5 分钟 = 50 分钟（用户体验差）

**触发条件**：
- 用户剩余额度 < 竞品数量
- 竞品关键词复杂（采集耗时 > 10 分钟）
- 并发限制（`analysis.Service` 的 semaphore 限 4 并发）

**影响**：
- 用户创建竞品分析失败（HTTP 402）
- 部分竞品采集超时，对比报告不完整

**缓解措施**：
1. **前置检查**（`competitor.Service.CreateAnalysis`）：
   ```go
   // 创建前检查额度
   budgetStatus, _ := s.credits.BudgetStatus(ctx, tenantID)
   if budgetStatus.Remaining < len(competitorIDs) {
       return "", pkgerrors.Wrap(pkgerrors.ErrPaymentRequired, 
           fmt.Sprintf("需要 %d 次额度，剩余 %d 次", len(competitorIDs), budgetStatus.Remaining))
   }
   ```

2. **限制竞品数量**：
   - API 校验：`len(competitor_ids) <= 5`（降低并发压力）
   - 前端提示："同时对比不超过 5 个竞品，以保证采集质量"

3. **超时保护**：
   - 单个竞品采集超时 → 标记为失败（`analysis_ids[i] = ""`）
   - 对比报告允许部分失败（至少 2 个成功即可生成）

4. **优先级队列**（可选）：
   - 竞品分析任务优先级高于普通分析（队列插队）
   - 需要改造 `queue.Queue` 接口（+1 天工作量）

**Plan B**：
如果性能问题严重（10 分钟+），改为**串行采集**：
- 不并发创建 N 个 analysis，改为顺序创建
- 用户体验下降，但稳定性提升
- 前端显示进度条："正在采集竞品 2/5..."

---

### R4: 检查点定时器延迟导致跟踪不及时

**描述**：  
检查点扫描器每小时运行一次，最坏情况下，用户设定的检查点延迟 59 分钟才触发。

**触发条件**：
- 用户设置检查点时间为 `10:00:00`
- 定时器上次运行时间为 `10:01:00`
- 下次运行时间为 `11:01:00`（延迟 1 小时）

**影响**：
- 用户期望"上线后 24 小时"立即触发，实际延迟 1 小时
- 业务价值下降（营销数据时效性要求高）

**缓解措施**：
1. **缩短扫描间隔**：
   - 生产环境改为 **30 分钟**（`time.NewTicker(30 * time.Minute)`）
   - 测试环境保持 1 小时（减少日志噪音）

2. **前端提示**：
   - 创建检查点时提示："检查点将在指定时间后 30 分钟内触发"
   - 详情页显示："预计触发时间：2026-10-02 10:00 ~ 10:30"

3. **事件驱动优化**（可选，+2 天）：
   - 检查点创建时，计算触发时间，设置 `time.AfterFunc`
   - 无需轮询，精确到秒
   - 需要持久化 timer 状态（重启后恢复）

**Plan B**：
如果延迟无法接受，改为**用户手动触发**：
- 检查点不自动触发，提供"立即执行"按钮
- 用户体验下降，但逻辑简化
- 工作量减少 0.5 天

---

### R5: 指数计算公式与业务预期偏差

**描述**：  
三大指数（情感/热度/风险）的计算公式是技术侧定义的，可能与产品经理/用户的直觉预期不一致。

**触发条件**：
- 用户看到情感指数 85 分，但负面评论很多（实际是中性占比高）
- 热度指数 95 分，但用户觉得声量不大（log 函数压缩了差异）

**影响**：
- 用户质疑数据准确性
- 需要返工调整公式（延期 1-2 天）

**缓解措施**：
1. **PRD 阶段对齐**（开发前）：
   - 用 5 个真实案例演示公式结果
   - 产品经理确认每个案例的预期分数
   - 调整公式直到双方对齐

2. **可配置化**（可选，+1 天）：
   - 公式参数存入 `platform_settings` 表：
     ```json
     {
       "sentiment_index_weight": [100, -50],
       "heat_index_log_factor": 20,
       "risk_index_sensitive_bonus": 10
     }
     ```
   - Admin UI 提供调整界面（重启生效）

3. **A/B 测试**：
   - 前端同时显示两个公式的结果（如"情感指数 v1: 78 / v2: 82"）
   - 收集用户反馈，选择更符合直觉的版本

**Plan B**：
如果公式争议大，**暂时隐藏指数面板**：
- 仅在详情页底部显示"实验性指标"
- 收集 1 周数据后再调整
- 工作量不变，但功能延后发布

---

## 时间风险

### R8: 工作量估算偏差（超期 50%+）

**描述**：  
技术设计文档估算总工作量 19-25 人天，但实际开发中常见以下超期因素：
- 需求理解偏差（返工 +2 天）
- 技术难题（LAC 集成、LLM 调优 +3 天）
- 测试与修复（集成测试发现 bug +2 天）

**影响**：
- 原计划 4 周，实际需要 6 周
- 延误其他需求排期

**缓解措施**：
1. **保守估算**：
   - 各需求工作量上浮 30%：
     - F23: 8-10 天 → **10-13 天**
     - F24: 6-8 天 → **8-10 天**
     - F25: 5-7 天 → **7-9 天**
   - 总计：25-32 天（**5-6.5 周**）

2. **每日站会**：
   - 每天同步进度，提前暴露延期风险
   - 阻塞问题当天解决（如 LAC 安装失败）

3. **并行开发**：
   - F25（指数/Geo）优先启动（无依赖）
   - F24/F23 并行开发（两个工程师）
   - 缩短整体周期到 4-5 周

4. **MVP 裁剪**：
   - 如果第 3 周进度 < 60%，砍掉非核心功能：
     - F23：去掉"跟踪报告"，仅保留策略生成
     - F24：去掉"五维雷达图"，仅保留情感/声量对比
     - F25：去掉"地图"，仅保留指数卡片

**Plan B**：
如果第 4 周进度 < 80%，**分阶段上线**：
- v0.2.0-alpha：仅 F25（指数/Geo）
- v0.2.0-beta：+ F24（竞品对比）
- v0.2.0-stable：+ F23（营销活动）

---

### R9: 三个需求相互阻塞（串行开发）

**描述**：  
如果开发资源不足（仅 1 个后端工程师），三个需求必须串行开发，耗时 19-25 天 → **8-10 周**。

**影响**：
- 严重延期，超出产品规划窗口
- 用户等待时间过长

**缓解措施**：
1. **资源协调**：
   - 至少分配 **2 个后端工程师**（1 个主力 + 1 个支援）
   - 分工：
     - 工程师 A：F23（营销活动，最复杂）
     - 工程师 B：F24 + F25（竞品对比 + 指数）

2. **前后端并行**：
   - 后端完成 API 后，立即交付前端（不等集成测试）
   - 前端用 Mock 数据先行开发

3. **复用现有能力**：
   - F24 复用 `analysis.Service`，无需新引擎
   - F25 复用 `insight_engine`，仅扩展 NER

**Plan B**：
如果资源不足，**放弃 F23**（营销活动）：
- F23 是三个需求中最复杂的（新引擎 + 定时器 + Pipeline）
- 仅实现 F24 + F25，工作量降至 11-15 天（**2.5-3 周**）
- F23 延后到 v0.3.0

---

## 依赖风险

### R6: 迁移脚本在生产环境执行失败

**描述**：  
数据库迁移是不可逆操作，脚本错误可能导致：
- 表结构损坏（无法回滚）
- 数据丢失（外键级联删除）
- 服务无法启动（列缺失）

**触发条件**：
- SQL 语法在 psql 通过，但 goose 分句报错（`$$` 块未加 `StatementBegin/End`）
- PG 版本差异（开发 PG 15，生产 PG 14）
- 并发迁移（两个工程师同时执行）

**影响**：
- **P0 生产事故**，需要紧急回滚
- 数据库锁表，服务中断

**缓解措施**：
1. **本地全流程测试**（开发阶段）：
   ```bash
   # 1. 清空测试库
   psql $YUQING_TEST_PG_URL -c "DROP DATABASE IF EXISTS yuqing_test; CREATE DATABASE yuqing_test;"
   
   # 2. 从 0001 迁移到 0010
   YUQING_CONFIG=config/test.yaml go run ./cmd/cli migrate platform
   
   # 3. 验证表结构
   psql yuqing_test -c "\d campaigns"
   psql yuqing_test -c "\d+ analyses" | grep sentiment_index
   ```

2. **生产事务试跑**（部署前）：
   ```bash
   # 在生产库（yuqing_platform）事务内执行，最后 ROLLBACK
   psql yuqing_platform <<EOF
   BEGIN;
   \i platform/migrations/platform/0009_campaigns_and_competitors.sql
   \i platform/migrations/platform/0010_indices_and_geo.sql
   SELECT * FROM campaigns LIMIT 0;  -- 验证表存在
   SELECT sentiment_index FROM analyses LIMIT 0;  -- 验证列存在
   ROLLBACK;
   EOF
   ```

3. **备份优先**（部署时）：
   ```bash
   # 迁移前备份整个数据库
   pg_dump yuqing_platform > /opt/backups/yuqing_platform_$(date +%Y%m%d_%H%M%S).sql
   
   # 验证备份文件大小 > 1MB
   ls -lh /opt/backups/yuqing_platform_*.sql
   ```

4. **迁移日志**：
   - 迁移输出保存到文件：`./bin/yuqing-cli migrate platform 2>&1 | tee migration.log`
   - 任何 ERROR 行都需要人工确认

**Plan B（回滚方案）**：
如果迁移失败，立即执行：
```bash
# 1. 恢复备份
psql yuqing_platform < /opt/backups/yuqing_platform_YYYYMMDD_HHMMSS.sql

# 2. 重启服务（用旧版本二进制）
systemctl restart yuqing-server

# 3. 确认服务可用
curl http://127.0.0.1:8080/health
```

---

### R10: 生产环境内存不足（LAC 模型加载）

**描述**：  
LAC 模型首次加载需要 ~200MB 内存，生产服务器（4C3.6Gi）已运行 7 个服务：
- Go server/worker: ~500MB × 2
- Python engines × 5: ~300MB × 5 = 1.5GB
- PG + Redis: ~500MB
- **总计**: ~3GB

新增 LAC 可能导致 OOM。

**触发条件**：
- `insight_engine` 首次调用 `LAC(mode='lac')` 时下载模型
- 多个并发请求同时加载模型

**影响**：
- insight_engine 进程被 OOM killer 杀死
- 分析任务失败率增加

**缓解措施**：
1. **预加载模型**（部署时）：
   ```bash
   # 激活 venv 后，手动触发模型下载
   cd /opt/yuqing/engines
   source venv/bin/activate
   python -c "from lac import LAC; LAC(mode='lac')"
   
   # 验证模型文件存在
   ls ~/.lac_model/  # 或检查 LAC 默认缓存目录
   ```

2. **内存监控**：
   ```bash
   # 部署后观察内存使用
   free -h
   ps aux --sort=-%mem | head -10
   
   # 如果可用内存 < 500MB，考虑优化：
   # - 减少 Go 连接池（cfg.DB.MaxConns: 20 → 10）
   # - 限制 Python worker 数量（uvicorn --workers 1）
   ```

3. **懒加载优化**（可选，+0.5 天）：
   ```python
   # 单例模式 + 懒加载
   _city_extractor = None
   
   def get_city_extractor():
       global _city_extractor
       if _city_extractor is None:
           _city_extractor = CityExtractor()
       return _city_extractor
   ```

**Plan B**：
如果内存不足，**禁用 LAC**：
- `insight_engine` 跳过 Geo 提取，返回空字典
- 前端不显示地图组件
- 工作量不变，但失去 F25 的 Geo 功能

---

## 运维风险

### 部署流程复杂度增加

**描述**：  
v0.2.0 新增：
- 1 个 Python 引擎（strategy_engine）
- 2 个数据库迁移（0009, 0010）
- 1 个 Python 依赖（lac）
- 2 个后台任务（检查点扫描器 + 竞品监听器）

部署步骤从 5 步增加到 9 步。

**影响**：
- 部署耗时增加（30 分钟 → 60 分钟）
- 出错概率增加

**缓解措施**：
1. **部署脚本**（`scripts/deploy_v0.2.0.sh`）：
   ```bash
   #!/bin/bash
   set -e
   
   echo "==> 1. 备份数据库"
   pg_dump yuqing_platform > /opt/backups/pre_v0.2.0.sql
   
   echo "==> 2. 上传二进制"
   # 本地执行：make build && scp bin/* user@server:/opt/yuqing/bin/
   
   echo "==> 3. 安装 Python 依赖"
   cd /opt/yuqing/engines
   source venv/bin/activate
   pip install lac==2.2.0
   python -c "from lac import LAC; LAC(mode='lac')"  # 预热模型
   
   echo "==> 4. 执行迁移"
   cd /opt/yuqing
   YUQING_CONFIG=/opt/yuqing/config/config.yaml ./bin/yuqing-cli migrate platform
   
   echo "==> 5. 启动新服务"
   systemctl start yuqing-strategy
   systemctl status yuqing-strategy
   
   echo "==> 6. 重启现有服务"
   systemctl restart yuqing-server yuqing-worker yuqing-insight
   
   echo "==> 7. 健康检查"
   curl http://127.0.0.1:8005/health
   curl http://127.0.0.1:8080/health
   
   echo "==> 部署完成"
   ```

2. **冒烟测试清单**（部署后）：
   - [ ] 创建分析任务 → 验证三大指数非零
   - [ ] 创建营销活动 → 验证策略生成
   - [ ] 创建竞品 → 创建竞品组 → 触发对比
   - [ ] 查看地图 → 验证城市分布

3. **回滚清单**：
   - [ ] 恢复数据库备份
   - [ ] 停止 yuqing-strategy 服务
   - [ ] 回退到 v0.1.2 二进制
   - [ ] 重启所有服务

---

## 业务风险

### 用户不理解新功能

**描述**：  
三个新功能都是"高级功能"，用户可能不理解：
- "营销活动"与"舆情分析"的区别
- "竞品对比"需要先创建竞品定义
- "指数"的计算逻辑

**影响**：
- 功能使用率低（< 10% 用户使用）
- 用户反馈"功能复杂"

**缓解措施**：
1. **新手引导**（前端）：
   - 首次进入"营销活动"页面，显示 Modal 引导：
     ```
     营销活动是什么？
     基于舆情分析结果，AI 为您生成营销方案建议，并跟踪营销效果。
     
     [查看示例] [开始创建]
     ```

2. **Demo 视频**（产品）：
   - 录制 3 个 3 分钟视频，展示各功能使用场景
   - 嵌入到功能页面顶部

3. **预置数据**（可选）：
   - 新注册用户自动创建 1 个示例竞品组（"中型 SUV 对比"）
   - 1 个示例营销活动（"雅阁口碑优化"）
   - 用户可直接查看，理解功能

**Plan B**：
如果使用率 < 5%，**简化功能**：
- 营销活动：去掉"检查点"，仅保留"策略生成"
- 竞品对比：去掉"竞品组"，改为"临时对比"（不保存竞品定义）
- 指数：仅显示情感指数（最直观）

---

## 降级方案

### 场景1：Strategy Engine 不可用

**触发条件**：
- LLM 供应商故障（GLM API 503）
- strategy_engine 进程崩溃

**降级方案**：
1. **后端降级**：
   ```go
   // campaign/pipeline.go
   resp, err := p.strategyEng.GenerateStrategy(ctx, req)
   if err != nil {
       // 降级：使用模板化方案
       resp = &engine.StrategyResponse{
           Content:  generateTemplateStrategy(analysis),
           Summary:  "基于舆情数据的通用营销建议",
           Dimensions: extractDimensionsFromAnalysis(analysis),
       }
   }
   ```

2. **前端标识**：
   - 策略内容顶部显示"系统生成（非 AI）"标签
   - 用户可编辑并保存修改

### 场景2：LAC NER 不可用

**触发条件**：
- LAC 模型加载失败
- 内存不足

**降级方案**：
1. **跳过 Geo 提取**：
   ```python
   try:
       geo_distribution = city_extractor.extract_cities(texts)
   except Exception as e:
       logger.warning(f"LAC NER failed: {e}")
       geo_distribution = {}
   ```

2. **前端隐藏地图**：
   ```tsx
   {analysis.geo_distribution && Object.keys(analysis.geo_distribution).length > 0 && (
     <GeoChart distribution={analysis.geo_distribution} />
   )}
   ```

### 场景3：竞品采集部分失败

**触发条件**：
- 10 个竞品中，3 个超时/失败

**降级方案**：
1. **允许部分成功**：
   ```go
   // competitor/service.go
   if len(successfulResults) >= 2 {
       // 至少 2 个成功，可生成对比报告
       report := s.buildComparisonReport(ctx, tenantID, ca.GroupID, successfulResults)
       // 标记失败的竞品
       report["failed_competitors"] = failedNames
   }
   ```

2. **前端提示**：
   - 对比报告顶部显示："部分竞品采集失败（凯美瑞、天籁），对比结果可能不完整"

---

## 风险缓解措施（总结）

### 开发阶段

1. **技术预研**（Week 0）：
   - [ ] LAC 在真实舆情数据上的准确率测试（目标 ≥ 70%）
   - [ ] LLM 策略生成质量测试（10 个场景，目标 ≥ 60% 可用）
   - [ ] 内存压测（模拟 LAC 加载 + 10 并发分析）

2. **每日同步**（Week 1-4）：
   - [ ] 每天 15:00 站会，同步进度和阻塞
   - [ ] 阻塞问题当天解决或升级

3. **Code Review**（每周）：
   - [ ] 迁移脚本：必须经过 psql 事务试跑
   - [ ] Pipeline 逻辑：必须有降级分支
   - [ ] 前端组件：必须处理空数据

### 测试阶段

1. **契约测试全覆盖**：
   - [ ] 所有新 API 端点进入 `contract_test.go`
   - [ ] Gap Registry 更新（已知问题显式标注）

2. **集成测试关键路径**：
   - [ ] 营销活动全链路（创建 → 策略生成 → 检查点 → 报告）
   - [ ] 竞品对比全链路（竞品 → 竞品组 → 对比分析 → 报告）
   - [ ] 指数计算验证（10 个真实分析，人工对比预期）

3. **E2E 冒烟测试**：
   - [ ] 生产环境 27 个现有用例全通过
   - [ ] 新增 v0.2.0 用例 9 个（3 个功能 × 3 个关键路径）

### 部署阶段

1. **部署前检查**：
   - [ ] 数据库备份完成
   - [ ] 迁移脚本事务试跑通过
   - [ ] 二进制文件 MD5 校验

2. **部署后监控**（首 24 小时）：
   - [ ] 错误日志（`grep ERROR /var/log/yuqing/*.log`）
   - [ ] 内存使用（`free -h` 每小时）
   - [ ] 服务健康（`systemctl status yuqing-*`）

3. **回滚准备**：
   - [ ] 备份文件路径记录在部署日志
   - [ ] 回滚脚本测试通过
   - [ ] 紧急联系人 on-call（首 48 小时）

---

## 附录：风险决策树

```
┌─────────────────────────┐
│ 开发进度 < 60%（Week 3）│
└─────────┬───────────────┘
          │
    Yes ──┼── 裁剪 MVP：去掉非核心功能
          │   - F23 去掉跟踪报告
          │   - F24 去掉五维雷达
          │   - F25 去掉地图
          │
    No ───┴── 继续开发

┌─────────────────────────┐
│ LAC 准确率 < 50%        │
└─────────┬───────────────┘
          │
    Yes ──┼── Plan B：用户自定义地域标签
          │   （放弃 NER）
          │
    No ───┴── 继续使用 LAC

┌─────────────────────────┐
│ LLM 生成成功率 < 60%    │
└─────────┬───────────────┘
          │
    Yes ──┼── Plan B：移除 AI 生成
          │   （用户手动填写方案）
          │
    No ───┴── 继续使用 LLM

┌─────────────────────────┐
│ 迁移脚本执行失败        │
└─────────┬───────────────┘
          │
    Yes ──┼── 立即回滚：
          │   1. 恢复备份
          │   2. 重启旧版本
          │   3. 通知用户
          │
    No ───┴── 继续部署

┌─────────────────────────┐
│ 生产内存不足（OOM）     │
└─────────┬───────────────┘
          │
    Yes ──┼── Plan B：禁用 LAC
          │   （geo_distribution 返回空）
          │
    No ───┴── 继续运行
```

---

**文档版本**: v1.0  
**最后更新**: 2026-09-25  
**审核状态**: 待评审  
**风险等级**: 整体 **P1**（可控，需密切监控）
