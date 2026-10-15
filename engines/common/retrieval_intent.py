"""Versioned, metered research planning from every keyword in one accepted run."""
import json
from typing import Mapping

from engines.common.model_budget import chat_json_bounded, glm_reasoning_options

VERSION = "semantic-v2"


def validate_intent(value, request, model):
    if not isinstance(value, Mapping) or not isinstance(value.get("topic"), str) or not value["topic"].strip():
        raise ValueError("research_intent_topic_required")
    queries = value.get("search_queries")
    if not isinstance(queries, list) or not 1 <= len(queries) <= 8:
        raise ValueError("research_intent_queries_required")
    if any(not isinstance(query, str) or not query.strip() or len(query) > 300 for query in queries):
        raise ValueError("research_intent_invalid_query")
    lists = {}
    for name in ("entities", "aliases", "relations", "focus", "non_assumptions"):
        items = value.get(name, [])
        if not isinstance(items, list) or len(items) > 32 or any(not isinstance(item, str) or len(item) > 1000 for item in items):
            raise ValueError("research_intent_invalid_list")
        lists[name] = items
    # Planning cannot invent a new supposedly equivalent model name. Only
    # orthographic aliases are permitted before source evidence exists.
    compact = lambda word: "".join(word.casefold().split())
    originals = {compact(word) for word in request.keywords}
    lists["aliases"] = [item for item in lists["aliases"] if compact(item) in originals]
    return {"version": VERSION, "original_keywords": list(request.keywords), "research_question": request.research_question,
            "title": request.title, "analysis_type": request.analysis_type, "scenario_template_id": request.scenario_template_id,
            "topic": value["topic"].strip()[:1000], **lists,
            "search_queries": list(dict.fromkeys(query.strip() for query in queries)), "model": model}


async def plan_research_intent(llm, model, request, *, deadline):
    prompt = {
        "original_keywords": request.keywords, "research_question": request.research_question,
        "title_auxiliary_only": request.title, "analysis_type": request.analysis_type,
        "scenario_template_id": request.scenario_template_id,
        "rules": [
            "全部关键词共同描述同一次研究，保留全部对象、型号和关注点的含义，不把它们拆成无关任务。",
            "研究可包含系列型号、别名、对象与关注点或竞品比较；不要要求材料逐字包含所有关键词。",
            "任务名称仅辅助，test等显示名不是研究对象。不得凭记忆确认型号、发布事实或新同义名。",
            "aliases只能列出原输入的大小写或空格写法，不能把iphone18max改成iphone18pro max。",
            "search_queries给出1至8个有区别的主题/实体/关注点表达，保证所有用户关键词含义都纳入研究。",
        ],
        "output": {"topic": "统一主题", "entities": [], "relations": [], "aliases": [], "focus": [],
                   "non_assumptions": [], "search_queries": []},
    }
    output = await chat_json_bounded(llm, model, [
        {"role": "system", "content": "你是盘古舆情研究规划员。关键词是同一研究主题的关联线索。只输出JSON对象，不补事实。"},
        {"role": "user", "content": json.dumps(prompt, ensure_ascii=False)},
    ], deadline=deadline, temperature=0, **glm_reasoning_options(model),
       usage_context={"run_id": request.run_id, "engine": "query", "phase": "plan"})
    return validate_intent(output, request, model)
