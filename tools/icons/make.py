#!/usr/bin/env python3
"""Draw PinPoint's app icons into internal/web/static/.

    python tools/icons/make.py

A white map pin on the brand blue, filling the safe zone of a maskable icon.
Needs Pillow (python -m pip install pillow).
"""
from pathlib import Path
from PIL import Image, ImageDraw

STATIC = Path(__file__).resolve().parents[2] / "internal" / "web" / "static"
BLUE = (32, 107, 196)


def icon(size):
    s = 4  # draw large, then shrink, for smooth edges
    n = size * s
    img = Image.new("RGB", (n, n), BLUE)
    d = ImageDraw.Draw(img)
    cx, cy, r = n / 2, n * 0.42, n * 0.2
    # The pin: a circle with a point below it.
    d.ellipse([cx - r, cy - r, cx + r, cy + r], fill="white")
    d.polygon([(cx - r * 0.86, cy + r * 0.5), (cx + r * 0.86, cy + r * 0.5), (cx, cy + r * 2.05)], fill="white")
    hole = r * 0.42
    d.ellipse([cx - hole, cy - hole, cx + hole, cy + hole], fill=BLUE)
    return img.resize((size, size), Image.LANCZOS)


for name, size in [("icon-192.png", 192), ("icon-512.png", 512), ("apple-touch-icon.png", 180)]:
    icon(size).save(STATIC / name, optimize=True)
    print("wrote", name)
