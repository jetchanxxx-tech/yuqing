"""Bundle portable materials after cloud document and browser acceptance."""
import hashlib
import json
import os
import shutil
import zipfile
from html.parser import HTMLParser
from pathlib import Path
from urllib.parse import unquote, urlsplit

if os.environ.get("GITHUB_ACTIONS") != "true" or os.environ.get("RUNNER_ENVIRONMENT") != "github-hosted":
    raise SystemExit("Marketing packaging requires the GitHub hosted workflow")

ROOT = Path(__file__).resolve().parents[2]
validation = json.loads((ROOT / "test-results/marketing/deck-validation.json").read_text())
assert validation["source_commit"] == os.environ["GITHUB_SHA"] and validation["total_slides"] == 26
browser = json.loads((ROOT / "test-results/marketing/browser-results.json").read_text())
assert browser["stats"]["unexpected"] == 0 and browser["stats"]["expected"] > 0 and browser["stats"]["skipped"] == 0
artifact = ROOT / "marketing-artifacts"
bundle = artifact / "pangu-marketing-0.2.5"
bundle.mkdir(parents=True, exist_ok=True)
for folder, names in {"landing": ["index.html", "index-redesign.html"], "demo": ["demo.html", "index.html"]}.items():
    target = bundle / folder
    target.mkdir(parents=True, exist_ok=True)
    for name in names:
        shutil.copyfile(ROOT / folder / name, target / name)
shutil.copytree(ROOT / "docs/marketing", bundle / "docs/marketing", dirs_exist_ok=True)
shutil.copytree(ROOT / "test-results/marketing", bundle / "verification", dirs_exist_ok=True)
(bundle / "demo.html").write_text((ROOT / "demo/demo.html").read_text().replace('../landing/index.html', 'landing/index.html').replace('../docs/marketing/', 'docs/marketing/'))
(bundle / "official-website.html").write_text((ROOT / "landing/index.html").read_text().replace('../demo/demo.html', 'demo/demo.html').replace('../docs/marketing/', 'docs/marketing/'))
(bundle / "index.html").write_text('''<!doctype html><html lang="zh-CN"><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>盘古舆情 · 宣传素材</title><style>body{max-width:760px;margin:8vh auto;padding:24px;font:17px/1.7 "Microsoft YaHei",sans-serif;color:#303034}a{color:#b71932}li{margin:16px 0}</style><h1>盘古舆情 · 0.2.5-beta 宣传素材</h1><p>官网、离线 Demo 与三份可编辑 PPT。案例及演示数据为虚构。</p><ul><li><a href="landing/index.html">官网介绍</a></li><li><a href="demo/demo.html">离线交互 Demo</a></li><li><a href="docs/marketing/index.html">三份 PPT、PDF 与 HTML 预览</a></li></ul><p>从本目录打开。网页无须启动服务；注册、登录入口需要网络。此素材包不表示已发布到生产网站。</p></html>''')
(bundle / "BUILD_COMMIT").write_text(os.environ["GITHUB_SHA"] + "\n")

class PortableLinks(HTMLParser):
    def __init__(self, source):
        super().__init__()
        self.source = source

    def handle_starttag(self, tag, attrs):
        for name, value in attrs:
            if name not in ("href", "src") or not value:
                continue
            link = urlsplit(value)
            if link.scheme or link.netloc or not link.path:
                continue
            target = (self.source.parent / unquote(link.path)).resolve()
            assert target.is_relative_to(bundle.resolve()) and target.exists(), f"Broken portable link: {self.source.relative_to(bundle)} -> {value}"

for source in bundle.rglob("*.html"):
    if "verification" not in source.relative_to(bundle).parts:
        PortableLinks(source).feed(source.read_text())
files = sorted(path for path in bundle.rglob("*") if path.is_file() and path.name != "SHA256SUMS")
(bundle / "SHA256SUMS").write_text("".join(f"{hashlib.sha256(path.read_bytes()).hexdigest()}  {path.relative_to(bundle).as_posix()}\n" for path in files))
archive = artifact / "pangu-marketing-0.2.5.zip"
with zipfile.ZipFile(archive, "w", zipfile.ZIP_DEFLATED) as handle:
    for path in sorted(bundle.rglob("*")):
        if path.is_file():
            handle.write(path, path.relative_to(artifact))
(artifact / (archive.name + ".sha256")).write_text(f"{hashlib.sha256(archive.read_bytes()).hexdigest()}  {archive.name}\n")
print(json.dumps({"source": os.environ["GITHUB_SHA"], "browser_checks": browser["stats"]["expected"],
                  "slides": 26, "archive": str(archive), "files": len(files)}))
