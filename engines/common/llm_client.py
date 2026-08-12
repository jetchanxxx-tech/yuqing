"""Shared LLM client (OpenAI-compatible API)."""
import httpx


class LLMClient:
    """Thin wrapper around an OpenAI-compatible chat completions endpoint."""

    def __init__(self, base_url: str, api_key: str = "", timeout: float = 120.0):
        self.base_url = base_url.rstrip("/")
        self.api_key = api_key
        self.timeout = timeout

    async def chat(self, model: str, messages: list[dict], **kwargs) -> dict:
        """Send a chat completion request. Returns the raw JSON response."""
        async with httpx.AsyncClient(timeout=self.timeout) as client:
            resp = await client.post(
                f"{self.base_url}/chat/completions",
                headers={"Authorization": f"Bearer {self.api_key}"},
                json={"model": model, "messages": messages, **kwargs},
            )
            resp.raise_for_status()
            return resp.json()
