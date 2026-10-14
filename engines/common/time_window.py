"""Display and prompt scope for a persisted, validated analysis date window."""


def analysis_period(date_from: str, date_to: str) -> str:
    if date_from and date_to:
        return f"{date_from} 至 {date_to}"
    if date_from:
        return f"{date_from} 起"
    if date_to:
        return f"截至 {date_to}"
    return ""


def analysis_scope(date_from: str, date_to: str) -> str:
    period = analysis_period(date_from, date_to)
    if not period:
        return ""
    return (
        f"分析时间范围：{period}（包含起止日期）。\n"
        "结论、情感和话题仅依据本次范围内已通过日期筛选的材料。"
        "材料提及的过往事件只能作为明确标注的历史背景，不得当成本期新发生的事件；"
        "不要补充范围外的新闻或把本次搜索样本当作完整周期的全网数据。\n"
    )
