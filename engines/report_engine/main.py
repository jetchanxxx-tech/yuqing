"""Report Engine — Template IR → HTML/PDF/MD/DOCX generation."""
from fastapi import FastAPI

app = FastAPI(title="Report Engine", version="0.1.0")


@app.get("/health")
async def health():
    return {"status": "ok", "engine": "report"}


@app.post("/generate")
async def generate(req: dict):
    # TODO: Template selection → IR → renderers (HTML/PDF/MD/DOCX).
    return {"report_id": "", "file_key": "", "format": ""}
