"""
fixtures.py — Synthetic and real test fixture management.

Fixtures are stored in tests/ai-evaluation/fixtures/ground_truth/*.txt
(one file per test case, UTF-8 encoded).

Test images are in fixtures/test_images/*.{png,jpg,pdf}.
The filename stem must match the ground truth file stem:
  ground_truth/amharic_printed_001.txt
  test_images/amharic_printed_001.png

SRS 7.2 requirements implemented here:
  - Documented provenance and permission (in fixtures/PROVENANCE.md)
  - Stratified by: language, print/handwriting, quality, font, domain
  - Separate train/val/test splits (this harness only ever uses test/)
"""

import json
import logging
from dataclasses import dataclass, field
from pathlib import Path
from typing import Iterator

logger = logging.getLogger(__name__)

# ─── Paths ────────────────────────────────────────────────────────────────────

BASE_DIR    = Path(__file__).parent.parent
GT_DIR      = BASE_DIR / "fixtures" / "ground_truth"
IMAGE_DIR   = BASE_DIR / "fixtures" / "test_images"
AUDIO_DIR   = BASE_DIR / "fixtures" / "test_audio"
REPORTS_DIR = BASE_DIR / "reports"


# ─── Fixture descriptor ───────────────────────────────────────────────────────

@dataclass
class TestFixture:
    """A single evaluation test case."""
    fixture_id: str          # stem, e.g. "amharic_printed_001"
    language: str            # ISO 639 code: "amh", "tir", "orm", "eng"
    document_class: str      # "printed", "handwritten", "degraded", "mixed"
    font_info: str           # e.g. "GFF Abyssinica SIL"
    scan_dpi: int            # e.g. 300
    domain: str              # "government", "academic", "news", "legal"
    ground_truth_path: Path
    image_path: Path
    notes: str = ""
    permitted: bool = True   # from PROVENANCE.md


@dataclass
class FixtureRegistry:
    """Registry of all available test fixtures."""
    fixtures: list[TestFixture] = field(default_factory=list)

    def filter(
        self,
        language: str | None = None,
        document_class: str | None = None,
        domain: str | None = None,
    ) -> list[TestFixture]:
        result = self.fixtures
        if language:
            result = [f for f in result if f.language == language]
        if document_class:
            result = [f for f in result if f.document_class == document_class]
        if domain:
            result = [f for f in result if f.domain == domain]
        return result

    def by_id(self, fixture_id: str) -> TestFixture | None:
        for f in self.fixtures:
            if f.fixture_id == fixture_id:
                return f
        return None


# ─── Fixture discovery ────────────────────────────────────────────────────────

def discover_fixtures(gt_dir: Path = GT_DIR, image_dir: Path = IMAGE_DIR) -> FixtureRegistry:
    """
    Discover all test fixtures by scanning the ground_truth directory.

    Each .txt file must have a corresponding image in test_images/.
    Metadata is read from a sidecar .json file (same stem) if present,
    otherwise defaults are used.
    """
    registry = FixtureRegistry()

    if not gt_dir.exists():
        logger.warning("Ground truth directory not found: %s", gt_dir)
        return registry

    for gt_file in sorted(gt_dir.glob("*.txt")):
        stem = gt_file.stem

        # Find matching image (pdf, png, jpg, tiff, webp)
        image_path = None
        for ext in (".pdf", ".png", ".jpg", ".jpeg", ".tiff", ".webp"):
            candidate = image_dir / (stem + ext)
            if candidate.exists():
                image_path = candidate
                break

        if image_path is None:
            logger.debug("No image found for ground truth: %s — skipping", stem)
            continue

        # Load sidecar metadata if present
        meta = _load_meta(gt_dir / (stem + ".json"))

        fixture = TestFixture(
            fixture_id     = stem,
            language       = meta.get("language", _infer_language(stem)),
            document_class = meta.get("document_class", _infer_class(stem)),
            font_info      = meta.get("font_info", "unknown"),
            scan_dpi       = meta.get("scan_dpi", 300),
            domain         = meta.get("domain", "general"),
            ground_truth_path = gt_file,
            image_path     = image_path,
            notes          = meta.get("notes", ""),
            permitted      = meta.get("permitted", True),
        )
        if fixture.permitted:
            registry.fixtures.append(fixture)
        else:
            logger.warning("Fixture %s marked as not permitted — skipping", stem)

    logger.info("Discovered %d test fixtures", len(registry.fixtures))
    return registry


def load_ground_truth(fixture: TestFixture) -> str:
    """Read the ground truth text for a fixture (UTF-8)."""
    return fixture.ground_truth_path.read_text(encoding="utf-8").strip()


def iter_fixtures(registry: FixtureRegistry) -> Iterator[tuple[TestFixture, str]]:
    """Iterate (fixture, ground_truth_text) pairs."""
    for f in registry.fixtures:
        try:
            gt = load_ground_truth(f)
            yield f, gt
        except Exception as exc:
            logger.error("Could not load ground truth for %s: %s", f.fixture_id, exc)


# ─── Synthetic fixture generation ─────────────────────────────────────────────

def generate_synthetic_fixtures(output_dir: Path | None = None) -> list[TestFixture]:
    """
    Generate synthetic test images with known Amharic text for baseline testing.
    Uses Pillow to render text — requires an Ethiopic-capable font to be installed.
    Falls back to ASCII if font is unavailable.

    These are used only for CI smoke tests, not for reported benchmarks.
    """
    try:
        from PIL import Image, ImageDraw, ImageFont
    except ImportError:
        logger.warning("Pillow not available — skipping synthetic fixture generation")
        return []

    out_dir = output_dir or (IMAGE_DIR / "synthetic")
    gt_out  = output_dir or (GT_DIR / "synthetic")
    out_dir.mkdir(parents=True, exist_ok=True)
    gt_out.mkdir(parents=True, exist_ok=True)

    # Sample Amharic sentences (publicly known text — no copyright issues)
    samples = [
        {
            "id":    "synthetic_amh_simple_001",
            "text":  "ሰላም\nአሜሪካ\nኢትዮጵያ",
            "label": "simple Amharic words",
        },
        {
            "id":    "synthetic_amh_sentence_001",
            "text":  "ይህ ሰነድ ለሙከራ ዓላማ ብቻ ነው",
            "label": "short Amharic sentence",
        },
        {
            "id":    "synthetic_ascii_001",
            "text":  "EthioScript AI Evaluation\nLine two of test",
            "label": "ASCII fallback test",
        },
    ]

    generated: list[TestFixture] = []
    for sample in samples:
        # Create white 800x200 image
        img = Image.new("RGB", (800, 200), color=(255, 255, 255))
        draw = ImageDraw.Draw(img)

        try:
            # Try to load an Ethiopic font; fall back to default
            font = ImageFont.truetype("/usr/share/fonts/truetype/abyssinica/AbyssinicaSIL-R.ttf", 36)
        except (OSError, IOError):
            font = ImageFont.load_default()

        draw.text((20, 20), sample["text"], fill=(0, 0, 0), font=font)

        img_path = out_dir / f"{sample['id']}.png"
        gt_path  = gt_out  / f"{sample['id']}.txt"

        img.save(str(img_path))
        gt_path.write_text(sample["text"], encoding="utf-8")

        meta_path = gt_out / f"{sample['id']}.json"
        meta_path.write_text(
            json.dumps({
                "language":       "amh",
                "document_class": "synthetic",
                "font_info":      "AbyssinicaSIL or default",
                "scan_dpi":       96,
                "domain":         "synthetic",
                "notes":          sample["label"],
                "permitted":      True,
            }, ensure_ascii=False, indent=2),
            encoding="utf-8",
        )

        generated.append(TestFixture(
            fixture_id        = sample["id"],
            language          = "amh",
            document_class    = "synthetic",
            font_info         = "synthetic",
            scan_dpi          = 96,
            domain            = "synthetic",
            ground_truth_path = gt_path,
            image_path        = img_path,
            notes             = sample["label"],
        ))

    logger.info("Generated %d synthetic fixtures", len(generated))
    return generated


# ─── Helpers ──────────────────────────────────────────────────────────────────

def _load_meta(path: Path) -> dict:
    if path.exists():
        try:
            return json.loads(path.read_text(encoding="utf-8"))
        except Exception:
            pass
    return {}


def _infer_language(stem: str) -> str:
    stem_lower = stem.lower()
    if "amh" in stem_lower:
        return "amh"
    if "tir" in stem_lower:
        return "tir"
    if "orm" in stem_lower:
        return "orm"
    return "amh"   # default


def _infer_class(stem: str) -> str:
    stem_lower = stem.lower()
    if "hand" in stem_lower:
        return "handwritten"
    if "degrad" in stem_lower or "low" in stem_lower:
        return "degraded"
    if "mixed" in stem_lower:
        return "mixed"
    return "printed"
