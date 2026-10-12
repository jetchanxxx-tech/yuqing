"""Render editable marketing decks and preserve exact-source review evidence.

Run only in build-release.yml on a GitHub hosted runner. It is not release
acceptance and never starts the application or contacts a provider.
"""
import json
import os
import re
import subprocess
import zipfile
from pathlib import Path
from xml.etree import ElementTree as ET

if os.environ.get("GITHUB_ACTIONS") != "true" or os.environ.get("RUNNER_ENVIRONMENT") != "github-hosted":
    raise SystemExit("Document rendering is restricted to the GitHub hosted workflow")

from PIL import Image, ImageDraw

ROOT = Path(__file__).resolve().parents[2]
OUT = ROOT / "docs/marketing"
EVIDENCE = ROOT / "test-results/marketing"
EVIDENCE.mkdir(parents=True, exist_ok=True)
NS = {"p": "http://schemas.openxmlformats.org/presentationml/2006/main",
      "a": "http://schemas.openxmlformats.org/drawingml/2006/main"}
expected = {"customer": 6, "partner": 10, "investor": 10}
result = {"source_commit": os.environ["GITHUB_SHA"], "decks": [], "total_slides": 26}

for audience, count in expected.items():
    stem = f"pangu-{audience}-0.2.5"
    source = OUT / f"{stem}.pptx"
    with zipfile.ZipFile(source) as archive:
        assert archive.testzip() is None, f"Corrupt PPTX: {source}"
        slides = sorted(name for name in archive.namelist() if re.fullmatch(r"ppt/slides/slide\d+\.xml", name))
        assert len(slides) == count, f"{audience} must contain {count} slides"
        presentation = ET.fromstring(archive.read("ppt/presentation.xml"))
        size = presentation.find("p:sldSz", NS)
        assert size is not None
        width, height = int(size.get("cx")), int(size.get("cy"))
        assert abs(width / height - 16 / 9) < 0.01, "Deck must be 16:9"
        editable_text_shapes = 0
        for slide in slides:
            xml = ET.fromstring(archive.read(slide))
            texts = xml.findall(".//a:t", NS)
            assert len(texts) >= 3, f"Slide lacks editable content: {slide}"
            editable_text_shapes += len(xml.findall(".//p:sp[p:txBody]", NS))
            # PowerPoint shapes must fit the canvas; rendering also catches font
            # wrapping that geometry alone cannot certify.
            for transform in xml.findall(".//a:xfrm", NS):
                offset, extent = transform.find("a:off", NS), transform.find("a:ext", NS)
                if offset is None or extent is None:
                    continue
                x, y, cx, cy = (int(offset.get("x")), int(offset.get("y")),
                                int(extent.get("cx")), int(extent.get("cy")))
                assert x >= -9144 and y >= -9144 and x + cx <= width + 9144 and y + cy <= height + 9144, f"Shape outside canvas: {slide}"
        assert len([name for name in archive.namelist() if re.fullmatch(r"ppt/notesSlides/notesSlide\d+\.xml", name)]) == count
    subprocess.run(["soffice", "-env:UserInstallation=file:///tmp/yuqing-marketing-office", "--headless",
                    "--convert-to", "pdf", "--outdir", str(OUT), str(source)], check=True, timeout=180)
    pdf = OUT / f"{stem}.pdf"
    info = subprocess.run(["pdfinfo", str(pdf)], check=True, capture_output=True, text=True).stdout
    assert int(re.search(r"^Pages:\s+(\d+)", info, re.M).group(1)) == count
    previews = OUT / "previews" / audience
    previews.mkdir(parents=True, exist_ok=True)
    subprocess.run(["pdftoppm", "-png", "-scale-to", "1440", str(pdf), str(previews / "slide")], check=True, timeout=120)
    images = sorted(previews.glob("slide-*.png"), key=lambda item: int(item.stem.split("-")[-1]))
    assert len(images) == count
    columns = 3
    thumb_w, thumb_h, label_h = 480, 270, 30
    sheet = Image.new("RGB", (columns * thumb_w, ((count + columns - 1) // columns) * (thumb_h + label_h)), "#ededeb")
    draw = ImageDraw.Draw(sheet)
    for index, image_file in enumerate(images):
        with Image.open(image_file) as image:
            assert image.width > 1000 and image.height > 500
            sheet.paste(image.convert("RGB").resize((thumb_w, thumb_h)), ((index % columns) * thumb_w, (index // columns) * (thumb_h + label_h)))
        draw.text(((index % columns) * thumb_w + 14, (index // columns) * (thumb_h + label_h) + thumb_h + 7),
                  f"{audience}  {index + 1}/{count}", fill="#303034")
    sheet.save(OUT / f"{stem}-contact.png")
    result["decks"].append({"audience": audience, "slides": count, "editable_text_shapes": editable_text_shapes,
                            "pptx": source.name, "pdf": pdf.name, "rendered_pages": len(images)})

(EVIDENCE / "deck-validation.json").write_text(json.dumps(result, indent=2) + "\n")
(OUT / "BUILD_COMMIT").write_text(os.environ["GITHUB_SHA"] + "\n")
print(json.dumps(result))
