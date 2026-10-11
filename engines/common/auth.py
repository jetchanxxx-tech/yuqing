"""Shared internal-auth middleware for Python engines."""
from fastapi import Request
from starlette.responses import JSONResponse
import hmac
from starlette.middleware.base import BaseHTTPMiddleware


class InternalAuthMiddleware(BaseHTTPMiddleware):
    """Validates X-Internal-Token against the configured secret."""

    def __init__(self, app, token: str):
        super().__init__(app)
        self.token = token

    async def dispatch(self, request: Request, call_next):
        if request.method == "GET" and request.url.path == "/health":
            return await call_next(request)
        if not hmac.compare_digest(request.headers.get("X-Internal-Token", ""), self.token):
            return JSONResponse(status_code=403, content={"detail": "forbidden"})
        return await call_next(request)
