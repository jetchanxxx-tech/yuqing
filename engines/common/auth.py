"""Shared internal-auth middleware for Python engines."""
from fastapi import Request, HTTPException
from starlette.middleware.base import BaseHTTPMiddleware


class InternalAuthMiddleware(BaseHTTPMiddleware):
    """Validates X-Internal-Token against the configured secret."""

    def __init__(self, app, token: str):
        super().__init__(app)
        self.token = token

    async def dispatch(self, request: Request, call_next):
        if request.headers.get("X-Internal-Token") != self.token:
            raise HTTPException(status_code=403, detail="forbidden")
        return await call_next(request)
