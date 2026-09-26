"""Database models for multi-agent debate system.

定义3张新表：debates, debate_metrics, llm_call_logs
"""

# ══════════════════════════════════════════════════════════
# 数据库表结构（SQL DDL）
# ══════════════════════════════════════════════════════════

# 注意：实际项目中这些表应该在Go Platform的数据库中创建
# 这里提供SQL定义供参考

SQL_CREATE_DEBATES = """
CREATE TABLE IF NOT EXISTS debates (
    id SERIAL PRIMARY KEY,
    analysis_id VARCHAR(50) NOT NULL,
    topic TEXT NOT NULL,
    status VARCHAR(20) NOT NULL DEFAULT 'pending',  -- pending, running, completed, failed
    rounds JSONB NOT NULL DEFAULT '[]',
    verdict TEXT,
    confidence FLOAT,
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT fk_analysis FOREIGN KEY (analysis_id)
        REFERENCES analyses(id) ON DELETE CASCADE
);

CREATE INDEX idx_debates_analysis_id ON debates(analysis_id);
CREATE INDEX idx_debates_status ON debates(status);
CREATE INDEX idx_debates_created_at ON debates(created_at);
"""


SQL_CREATE_DEBATE_METRICS = """
CREATE TABLE IF NOT EXISTS debate_metrics (
    id SERIAL PRIMARY KEY,
    debate_id INTEGER NOT NULL,
    total_tokens INTEGER NOT NULL,
    cost_cny DECIMAL(10, 4) NOT NULL,
    duration_ms INTEGER NOT NULL,
    similarity_avg FLOAT,
    similarity_max FLOAT,
    retry_count INTEGER DEFAULT 0,
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT fk_debate FOREIGN KEY (debate_id)
        REFERENCES debates(id) ON DELETE CASCADE
);

CREATE INDEX idx_debate_metrics_debate_id ON debate_metrics(debate_id);
CREATE INDEX idx_debate_metrics_created_at ON debate_metrics(created_at);
"""


SQL_CREATE_LLM_CALL_LOGS = """
CREATE TABLE IF NOT EXISTS llm_call_logs (
    id SERIAL PRIMARY KEY,
    debate_id INTEGER NOT NULL,
    agent_name VARCHAR(50) NOT NULL,  -- 主持人, 事实核查员, etc.
    round_number INTEGER NOT NULL,
    prompt TEXT NOT NULL,
    response TEXT NOT NULL,
    tokens_prompt INTEGER NOT NULL,
    tokens_completion INTEGER NOT NULL,
    tokens_total INTEGER NOT NULL,
    cost_cny DECIMAL(10, 6) NOT NULL,
    duration_ms INTEGER NOT NULL,
    status VARCHAR(20) NOT NULL,  -- success, timeout, error
    error_message TEXT,
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT fk_debate FOREIGN KEY (debate_id)
        REFERENCES debates(id) ON DELETE CASCADE
);

CREATE INDEX idx_llm_call_logs_debate_id ON llm_call_logs(debate_id);
CREATE INDEX idx_llm_call_logs_agent_name ON llm_call_logs(agent_name);
CREATE INDEX idx_llm_call_logs_created_at ON llm_call_logs(created_at);
"""


# ══════════════════════════════════════════════════════════
# Python数据类（供Go Platform调用）
# ══════════════════════════════════════════════════════════

from typing import List, Optional, Dict, Any
from datetime import datetime
from enum import Enum


class DebateStatus(str, Enum):
    """辩论状态枚举。"""
    PENDING = "pending"
    RUNNING = "running"
    COMPLETED = "completed"
    FAILED = "failed"


class LLMCallStatus(str, Enum):
    """LLM调用状态枚举。"""
    SUCCESS = "success"
    TIMEOUT = "timeout"
    ERROR = "error"


class DebateRecord:
    """辩论记录（对应debates表）。"""

    def __init__(
        self,
        id: int,
        analysis_id: str,
        topic: str,
        status: DebateStatus,
        rounds: List[Dict[str, Any]],
        verdict: Optional[str] = None,
        confidence: Optional[float] = None,
        created_at: Optional[datetime] = None,
        updated_at: Optional[datetime] = None
    ):
        self.id = id
        self.analysis_id = analysis_id
        self.topic = topic
        self.status = status
        self.rounds = rounds
        self.verdict = verdict
        self.confidence = confidence
        self.created_at = created_at or datetime.now()
        self.updated_at = updated_at or datetime.now()

    def to_dict(self) -> Dict[str, Any]:
        """转换为字典。"""
        return {
            "id": self.id,
            "analysis_id": self.analysis_id,
            "topic": self.topic,
            "status": self.status.value,
            "rounds": self.rounds,
            "verdict": self.verdict,
            "confidence": self.confidence,
            "created_at": self.created_at.isoformat() if self.created_at else None,
            "updated_at": self.updated_at.isoformat() if self.updated_at else None
        }


class DebateMetrics:
    """辩论性能指标（对应debate_metrics表）。"""

    def __init__(
        self,
        id: int,
        debate_id: int,
        total_tokens: int,
        cost_cny: float,
        duration_ms: int,
        similarity_avg: Optional[float] = None,
        similarity_max: Optional[float] = None,
        retry_count: int = 0,
        created_at: Optional[datetime] = None
    ):
        self.id = id
        self.debate_id = debate_id
        self.total_tokens = total_tokens
        self.cost_cny = cost_cny
        self.duration_ms = duration_ms
        self.similarity_avg = similarity_avg
        self.similarity_max = similarity_max
        self.retry_count = retry_count
        self.created_at = created_at or datetime.now()

    def to_dict(self) -> Dict[str, Any]:
        """转换为字典。"""
        return {
            "id": self.id,
            "debate_id": self.debate_id,
            "total_tokens": self.total_tokens,
            "cost_cny": self.cost_cny,
            "duration_ms": self.duration_ms,
            "similarity_avg": self.similarity_avg,
            "similarity_max": self.similarity_max,
            "retry_count": self.retry_count,
            "created_at": self.created_at.isoformat() if self.created_at else None
        }


class LLMCallLog:
    """LLM调用日志（对应llm_call_logs表）。"""

    def __init__(
        self,
        id: int,
        debate_id: int,
        agent_name: str,
        round_number: int,
        prompt: str,
        response: str,
        tokens_prompt: int,
        tokens_completion: int,
        tokens_total: int,
        cost_cny: float,
        duration_ms: int,
        status: LLMCallStatus,
        error_message: Optional[str] = None,
        created_at: Optional[datetime] = None
    ):
        self.id = id
        self.debate_id = debate_id
        self.agent_name = agent_name
        self.round_number = round_number
        self.prompt = prompt
        self.response = response
        self.tokens_prompt = tokens_prompt
        self.tokens_completion = tokens_completion
        self.tokens_total = tokens_total
        self.cost_cny = cost_cny
        self.duration_ms = duration_ms
        self.status = status
        self.error_message = error_message
        self.created_at = created_at or datetime.now()

    def to_dict(self) -> Dict[str, Any]:
        """转换为字典。"""
        return {
            "id": self.id,
            "debate_id": self.debate_id,
            "agent_name": self.agent_name,
            "round_number": self.round_number,
            "prompt": self.prompt[:200] + "..." if len(self.prompt) > 200 else self.prompt,
            "response": self.response[:200] + "..." if len(self.response) > 200 else self.response,
            "tokens_prompt": self.tokens_prompt,
            "tokens_completion": self.tokens_completion,
            "tokens_total": self.tokens_total,
            "cost_cny": self.cost_cny,
            "duration_ms": self.duration_ms,
            "status": self.status.value,
            "error_message": self.error_message,
            "created_at": self.created_at.isoformat() if self.created_at else None
        }


# ══════════════════════════════════════════════════════════
# 工具函数
# ══════════════════════════════════════════════════════════

def get_all_sql_statements() -> List[str]:
    """获取所有建表SQL语句。"""
    return [
        SQL_CREATE_DEBATES,
        SQL_CREATE_DEBATE_METRICS,
        SQL_CREATE_LLM_CALL_LOGS
    ]


if __name__ == "__main__":
    import sys
    import io

    # 修复Windows GBK编码问题
    if sys.platform == 'win32':
        sys.stdout = io.TextIOWrapper(sys.stdout.buffer, encoding='utf-8')

    print("[OK] Database models defined")
    print(f"\nSQL Statements:")
    for i, sql in enumerate(get_all_sql_statements(), 1):
        lines = sql.strip().split('\n')
        table_name = lines[0].split()[-1].replace('(', '')
        print(f"  {i}. {table_name}")

    print(f"\nPython Classes:")
    print(f"  - DebateRecord")
    print(f"  - DebateMetrics")
    print(f"  - LLMCallLog")
