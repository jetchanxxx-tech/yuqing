"""Test cases for multi-agent debate system.

10个真实舆情场景，用于验证Prompt质量和观点差异化。
"""

# ══════════════════════════════════════════════════════════
# 测试用例定义
# ══════════════════════════════════════════════════════════

TEST_CASES = [
    {
        "id": "case_01",
        "name": "雅阁后排空间争议",
        "topic": "本田雅阁后排空间虚假宣传事件",
        "summary": {
            "doc_count": 324,
            "time_range": "2024-03-15 至 2024-03-20",
            "platforms": "微博65%, 小红书20%, 抖音15%",
            "sentiment_positive": 22,
            "sentiment_negative": 78,
            "sentiment_neutral": 0,
            "top_keywords": ["后排空间", "虚假宣传", "车主维权", "退订", "官方回应"]
        },
        "expected_diversity": "事实核查应关注数据真实性，情绪分析应关注车主愤怒情绪，传播专家应关注微博主战场"
    },

    {
        "id": "case_02",
        "name": "花西子79元眉笔争议",
        "topic": "花西子眉笔定价争议与消费者情绪",
        "summary": {
            "doc_count": 586,
            "time_range": "2023-09-10 至 2023-09-15",
            "platforms": "抖音45%, 微博30%, 小红书25%",
            "sentiment_positive": 15,
            "sentiment_negative": 85,
            "sentiment_neutral": 0,
            "top_keywords": ["79元", "李佳琦", "哪里贵了", "国货之光", "消费降级"]
        },
        "expected_diversity": "情绪分析应识别阶层焦虑，传播专家应关注李佳琦直播间放大效应"
    },

    {
        "id": "case_03",
        "name": "海底捞老鼠事件",
        "topic": "海底捞后厨老鼠视频曝光",
        "summary": {
            "doc_count": 892,
            "time_range": "2017-08-25 至 2017-08-30",
            "platforms": "微博55%, 微信公众号30%, 知乎15%",
            "sentiment_positive": 8,
            "sentiment_negative": 92,
            "sentiment_neutral": 0,
            "top_keywords": ["老鼠", "后厨", "食品安全", "道歉", "整改"]
        },
        "expected_diversity": "事实核查应验证视频真实性，处置建议应强调48小时黄金窗口"
    },

    {
        "id": "case_04",
        "name": "小米续航虚标事件",
        "topic": "小米手机续航测试与宣传数据不符",
        "summary": {
            "doc_count": 423,
            "time_range": "2024-01-10 至 2024-01-15",
            "platforms": "知乎40%, B站30%, 微博30%",
            "sentiment_positive": 35,
            "sentiment_negative": 65,
            "sentiment_neutral": 0,
            "top_keywords": ["续航虚标", "实测数据", "硬核UP主", "理论值", "使用场景"]
        },
        "expected_diversity": "事实核查应区分理论值和实测值，传播专家应关注B站UP主影响力"
    },

    {
        "id": "case_05",
        "name": "K12机构退费纠纷",
        "topic": "某K12教育机构倒闭引发家长退费维权",
        "summary": {
            "doc_count": 765,
            "time_range": "2021-07-20 至 2021-07-25",
            "platforms": "微信群50%, 微博30%, 知乎20%",
            "sentiment_positive": 5,
            "sentiment_negative": 95,
            "sentiment_neutral": 0,
            "top_keywords": ["退费", "跑路", "双减政策", "家长维权", "课程转让"]
        },
        "expected_diversity": "情绪分析应关注家长焦虑和愤怒，处置建议应提出转课方案"
    },

    {
        "id": "case_06",
        "name": "理想冬季续航折损",
        "topic": "理想汽车冬季续航大幅缩水引发车主不满",
        "summary": {
            "doc_count": 534,
            "time_range": "2023-12-15 至 2023-12-20",
            "platforms": "汽车之家35%, 微博30%, 小红书20%, 抖音15%",
            "sentiment_positive": 25,
            "sentiment_negative": 75,
            "sentiment_neutral": 0,
            "top_keywords": ["冬季续航", "东北", "华南", "温差", "电池衰减"]
        },
        "expected_diversity": "传播专家应识别地域差异（东北vs华南），处置建议应区域化补偿"
    },

    {
        "id": "case_07",
        "name": "鸿星尔克野性消费",
        "topic": "鸿星尔克捐款引发网友野性消费",
        "summary": {
            "doc_count": 1247,
            "time_range": "2021-07-23 至 2021-07-28",
            "platforms": "抖音40%, 微博35%, 小红书25%",
            "sentiment_positive": 92,
            "sentiment_negative": 8,
            "sentiment_neutral": 0,
            "top_keywords": ["捐款", "野性消费", "国货支持", "直播间", "感动中国"]
        },
        "expected_diversity": "情绪分析应识别民族情感，处置建议应把握借势时机（正面舆情）"
    },

    {
        "id": "case_08",
        "name": "瑞幸咖啡财务造假",
        "topic": "瑞幸咖啡承认财务造假22亿",
        "summary": {
            "doc_count": 1834,
            "time_range": "2020-04-02 至 2020-04-07",
            "platforms": "微博45%, 财经媒体30%, 知乎25%",
            "sentiment_positive": 3,
            "sentiment_negative": 97,
            "sentiment_neutral": 0,
            "top_keywords": ["财务造假", "22亿", "浑水", "退市", "投资者损失"]
        },
        "expected_diversity": "事实核查应梳理造假链条，情绪分析应区分投资者愤怒和路人吃瓜"
    },

    {
        "id": "case_09",
        "name": "蜜雪冰城食安问题",
        "topic": "蜜雪冰城多地门店食品安全问题曝光",
        "summary": {
            "doc_count": 456,
            "time_range": "2022-05-15 至 2022-05-20",
            "platforms": "抖音40%, 微博35%, 小红书25%",
            "sentiment_positive": 12,
            "sentiment_negative": 88,
            "sentiment_neutral": 0,
            "top_keywords": ["食品安全", "加盟店", "过期原料", "整改", "品牌形象"]
        },
        "expected_diversity": "处置建议应区分加盟店和直营店责任，传播专家应关注抖音短视频传播"
    },

    {
        "id": "case_10",
        "name": "特斯拉刹车失灵争议",
        "topic": "特斯拉上海车展维权事件",
        "summary": {
            "doc_count": 2134,
            "time_range": "2021-04-19 至 2021-04-24",
            "platforms": "微博50%, 抖音25%, 知乎15%, 汽车之家10%",
            "sentiment_positive": 18,
            "sentiment_negative": 82,
            "sentiment_neutral": 0,
            "top_keywords": ["刹车失灵", "车展维权", "数据公开", "傲慢回应", "消费者权益"]
        },
        "expected_diversity": "情绪分析应关注特斯拉傲慢态度引发的情绪升级，传播专家应分析车展现场视频的病毒式传播"
    }
]


def get_test_case(case_id: str) -> dict:
    """根据ID获取测试用例。"""
    for case in TEST_CASES:
        if case["id"] == case_id:
            return case
    raise ValueError(f"Test case not found: {case_id}")


def get_all_case_ids() -> list:
    """获取所有测试用例ID。"""
    return [case["id"] for case in TEST_CASES]


if __name__ == "__main__":
    import sys
    import io

    # 修复Windows GBK编码问题
    if sys.platform == 'win32':
        sys.stdout = io.TextIOWrapper(sys.stdout.buffer, encoding='utf-8')

    print(f"[OK] Loaded {len(TEST_CASES)} test cases")
    print("\nTest Cases:")
    for case in TEST_CASES:
        print(f"  {case['id']}: {case['name']}")
        print(f"    - 文档数: {case['summary']['doc_count']}")
        print(f"    - 负面占比: {case['summary']['sentiment_negative']}%")
        print(f"    - 主战场: {case['summary']['platforms'].split(',')[0].strip()}")
