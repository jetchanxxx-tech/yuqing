"""Hard filters/quota/dedup on the actual authenticated semantic HTTP route."""
import pytest
from engines.tests.test_query_semantic_http import (
    TopicModel, accepted_request, post_search, provider_candidate, setup_pipeline,
)


@pytest.mark.asyncio
async def test_exclude_words_match_available_material_case_insensitively(monkeypatch):
    setup_pipeline(monkeypatch, {"toutiao": [
        provider_candidate("toutiao", 1, "iPhone18 Pro含有噪声推广词，这是一篇足够长的公开网页摘要。"),
        provider_candidate("toutiao", 2, "iPhone18 Max CLEAN BODY用户体验，续航和手感都有具体描述。"),
        provider_candidate("toutiao", 3, "iPhone18标准版体验不错，拍摄一小时后电池仍有余量。"),
    ]})
    response = await post_search(accepted_request(exclude_words=[" 噪声 ", "body"]))
    assert response.status_code == 200, response.text
    data = response.json()
    assert len(data["documents"]) == 1
    assert data["coverage"]["excluded_word_count"] == 2
    assert data["sources"][0]["doc_count"] == 1


@pytest.mark.asyncio
async def test_duplicate_selected_platform_and_same_content_do_not_double_count(monkeypatch):
    repeated = "iPhone18 Pro实测续航内容，长时间拍摄后电量仍然有余量。"
    calls, _ = setup_pipeline(monkeypatch, {"toutiao": [
        provider_candidate("toutiao", 1, repeated), provider_candidate("toutiao", 2, repeated),
        provider_candidate("toutiao", 3, "iPhone18 Max独立材料：手机握持偏重但阅读屏幕很舒服。"),
    ]})
    response = await post_search(accepted_request(sources=["toutiao", "toutiao"]))
    assert response.status_code == 200, response.text
    result = response.json()
    assert len(calls) == 1 and result["total_count"] == 2
    assert result["coverage"]["duplicate_count"] == 1
    assert len(result["sources"]) == 1


@pytest.mark.asyncio
@pytest.mark.parametrize("changes", [
    {"date_from": "bad"}, {"date_to": "2026-09-31"}, {"date_from": "2026-10-15", "date_to": "2026-10-01"},
    {"sources": ["xiaohongshu"]}, {"sources": ["unknown"]}, {"keywords": [f"word-{i}" for i in range(9)]},
    {"keywords": ["", " "]}, {"max_results": 0},
])
async def test_invalid_inputs_fail_before_paid_calls(monkeypatch, changes):
    calls, model = setup_pipeline(monkeypatch, {})
    response = await post_search(accepted_request(**changes))
    assert response.status_code == 422, response.text
    assert calls == [] and model.calls == []


@pytest.mark.asyncio
async def test_missing_search_key_never_uses_test_presets(monkeypatch):
    from engines.query_engine import main
    calls, model = setup_pipeline(monkeypatch, {})
    monkeypatch.setattr(main, "DEFAULT_BOCHA_KEY", "")
    monkeypatch.delenv("BOCHA_API_KEY")
    response = await post_search(accepted_request())
    assert response.status_code == 503
    assert not calls and not model.calls


@pytest.mark.asyncio
async def test_undated_material_allowed_only_when_date_window_not_requested(monkeypatch):
    setup_pipeline(monkeypatch, {"toutiao": [provider_candidate("toutiao", 1,
        "iPhone18 Pro真实体验材料，没有日期但包含实际续航与拍摄描述。", "")]})
    response = await post_search(accepted_request())
    assert response.status_code == 200, response.text
    assert response.json()["total_count"] == 1
    assert response.json()["coverage"]["date_filter"] == "not_requested"


@pytest.mark.asyncio
async def test_large_requested_result_limit_is_capped_to_fifty_after_semantic(monkeypatch):
    setup_pipeline(monkeypatch, {"toutiao": [provider_candidate("toutiao", i,
        f"iPhone18 Pro第{i}篇独立实际体验材料，本文记录续航与拍摄感受。") for i in range(70)]})
    response = await post_search(accepted_request(max_results=500))
    assert response.status_code == 200, response.text
    data = response.json()
    assert data["total_count"] == 50
    assert data["coverage"]["result_limit"] == 50
    assert data["coverage"]["truncated_count"] == 20
    assert len(data["coverage"]["candidate_audit"]) == 70


@pytest.mark.asyncio
async def test_one_platform_provider_failure_preserves_other_platform_results(monkeypatch):
    from engines.query_engine import main
    scraper_calls, _ = setup_pipeline(monkeypatch, {"wechat": [provider_candidate("wechat", 1,
        "iPhone18 Max真实体验，本文记录大屏手感和阅读体验，供本轮研究使用。")]})
    instance = main.get_scraper()
    existing = instance._bocha_search
    async def partial(*args, **kwargs):
        if kwargs["include"] == "toutiao.com":
            raise RuntimeError("fixture-secret-error-detail")
        return await existing(*args, **kwargs)
    monkeypatch.setattr(instance, "_bocha_search", partial)
    response = await post_search(accepted_request(sources=["toutiao", "wechat"]))
    assert response.status_code == 200, response.text
    result = response.json()
    assert result["total_count"] == 1
    assert result["coverage"]["keyword_search"] == "partial"
    assert "fixture-secret-error-detail" not in response.text
    assert result["coverage"]["per_platform"][0]["status"] == "failed"


@pytest.mark.asyncio
async def test_all_platform_failure_is_typed_error_with_durable_audit(monkeypatch):
    from engines.query_engine import main
    setup_pipeline(monkeypatch, {})
    async def failed(*args, **kwargs):
        raise RuntimeError("fixture-secret-error-detail")
    monkeypatch.setattr(main.get_scraper(), "_bocha_search", failed)
    response = await post_search(accepted_request())
    assert response.status_code == 502, response.text
    assert response.json()["detail"]["coverage"]["admission_version"] == "semantic-v2"
    assert "fixture-secret-error-detail" not in response.text


@pytest.mark.asyncio
async def test_unrequested_source_and_spoofed_domain_cannot_enter_semantic_pool(monkeypatch):
    wrong = provider_candidate("wechat", 1, "iPhone18 Pro未选公众号的候选，正文再相关也不能放行。")
    spoof = {**provider_candidate("toutiao", 2, "iPhone18 Pro伪造域名的候选，正文再相关也不能放行。"),
             "url": "https://toutiao.com.evil.org/2"}
    setup_pipeline(monkeypatch, {"toutiao": [wrong, spoof, provider_candidate("toutiao", 3,
        "iPhone18 Pro所选平台真实域名的内容，包含实际续航体验与观察。")]})
    response = await post_search(accepted_request())
    assert response.status_code == 200, response.text
    assert response.json()["total_count"] == 1
    assert response.json()["coverage"]["source_mismatch_count"] == 2
