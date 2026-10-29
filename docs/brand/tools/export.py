#!/usr/bin/env python3
"""Build Oh My Boot brand files from the master geometry and Geist Mono.

Usage: python3 export.py <GeistMono-Variable.woff2> <out-dir>

Needs: fonttools, brotli, Pillow, and Playwright with Chromium (only for the
PNG/ICO exports). The SVG files are written without any browser.
"""
import base64
import io
import os
import sys

from fontTools.pens.svgPathPen import SVGPathPen
from fontTools.pens.transformPen import TransformPen
from fontTools.ttLib import TTFont
from fontTools.varLib.instancer import instantiateVariableFont

# Palette (DESIGN.md, «Пульт»)
TILE = "#0E1013"       # plate on light backgrounds and in the boot menu
TILE_DARK = "#1F242C"  # plate on dark UI surfaces
RING = "#E6E8EB"
INK = "#14171C"        # text colour on light backgrounds
ACCENT = "#F2A93B"
MENU_BG = "#0E1013"    # colour 0 in internal/boot/menu.go

WORD = "oh my boot"
WGHT = 500
CELL = 600             # Geist Mono advance width, font units
UPM = 1000
XH = 530               # x-height, font units
CURSOR_W = CELL        # one character cell
CURSOR_TOP = 20        # above baseline, font units (CSS: 0.12em tall, -0.1em)
CURSOR_BOTTOM = -100

# Mark geometry on a 32-unit grid.
MARK = {
    # >= 24 px
    "main": dict(cx=11, cy=15, r=5.5, sw=3, bx=20, by=19, bw=8, bh=3, brx=0.5),
    # 16 px: 2 px strokes, cursor edges on the pixel grid
    "small": dict(cx=11, cy=15, r=5, sw=4, bx=20, by=18, bw=8, bh=4, brx=0),
}


def mark_shapes(kind, ring, cursor):
    g = MARK[kind]
    rx = f' rx="{g["brx"]}"' if g["brx"] else ""
    return (f'<circle cx="{g["cx"]}" cy="{g["cy"]}" r="{g["r"]}" fill="none" '
            f'stroke="{ring}" stroke-width="{g["sw"]}"/>'
            f'<rect x="{g["bx"]}" y="{g["by"]}" width="{g["bw"]}" height="{g["bh"]}"{rx} fill="{cursor}"/>')


def mark_svg(kind, plate, ring, cursor, size, title="Oh My Boot", square=False):
    rx = "" if square else ' rx="7"'
    plate_el = f'<rect width="32" height="32"{rx} fill="{plate}"/>' if plate else ""
    return (f'<svg xmlns="http://www.w3.org/2000/svg" width="{size}" height="{size}" viewBox="0 0 32 32">'
            f'<title>{title}</title>{plate_el}{mark_shapes(kind, ring, cursor)}</svg>\n')


def load_font(path):
    font = TTFont(path)
    inst = instantiateVariableFont(font, {"wght": WGHT})
    inst.flavor = None
    return inst


def word_path(font, scale, x0, baseline):
    """SVG path data for WORD; font units scaled, y flipped, origin at (x0, baseline)."""
    gs = font.getGlyphSet()
    cmap = font.getBestCmap()
    parts = []
    x = 0
    for ch in WORD:
        name = cmap[ord(ch)]
        pen = SVGPathPen(gs, ntos=lambda v: f"{round(v, 2):g}")
        tpen = TransformPen(pen, (scale, 0, 0, -scale, x0 + x * scale, baseline))
        gs[name].draw(tpen)
        d = pen.getCommands()
        if d:
            parts.append(d)
        x += font["hmtx"][name][0]
    return " ".join(parts)


def cursor_rect(scale, x0, baseline, colour):
    x = x0 + len(WORD) * CELL * scale
    y = baseline - CURSOR_TOP * scale
    h = (CURSOR_TOP - CURSOR_BOTTOM) * scale
    return f'<rect x="{x:.2f}" y="{y:.2f}" width="{CURSOR_W * scale:.2f}" height="{h:.2f}" fill="{colour}"/>'


def wordmark_svg(font, text, cursor, size=48):
    """Wordmark alone; `size` is the font size in px."""
    s = size / UPM
    asc, desc = 760, -220  # ink bounds of "ohmybt" incl. y descender, with margin
    w = (len(WORD) + 1) * CELL * s
    h = (asc - desc) * s
    base = asc * s
    d = word_path(font, s, 0, base)
    return (f'<svg xmlns="http://www.w3.org/2000/svg" width="{w:.0f}" height="{h:.0f}" viewBox="0 0 {w:.2f} {h:.2f}">'
            f'<title>Oh My Boot</title><path d="{d}" fill="{text}"/>{cursor_rect(s, 0, base, cursor)}</svg>\n')


def lockup_parts(font, mark_px, plate, ring, text, cursor, x0=0, y0=0):
    """Mark + wordmark. Gap = mark/2, font size = mark/1.45, x-height centred on the ring."""
    k = mark_px / 32
    fs = mark_px / 1.45
    s = fs / UPM
    ring_cy = y0 + MARK["main"]["cy"] * k
    base = ring_cy + XH * s / 2
    tx = x0 + mark_px + mark_px / 2
    d = word_path(font, s, tx, base)
    plate_el = f'<rect width="32" height="32" rx="7" fill="{plate}"/>' if plate else ""
    mark = (f'<g transform="translate({x0:.2f} {y0:.2f}) scale({k:g})">{plate_el}'
            f'{mark_shapes("main", ring, cursor)}</g>')
    width = tx + (len(WORD) + 1) * CELL * s - x0
    return mark + f'<path d="{d}" fill="{text}"/>' + cursor_rect(s, tx, base, cursor), width


def lockup_svg(font, plate, ring, text, cursor, mark_px=64):
    pad = mark_px / 4  # clear space: 1/4 of the mark height
    body, width = lockup_parts(font, mark_px, plate, ring, text, cursor, pad, pad)
    W, H = width + 2 * pad, mark_px + 2 * pad
    return (f'<svg xmlns="http://www.w3.org/2000/svg" width="{W:.0f}" height="{H:.0f}" viewBox="0 0 {W:.2f} {H:.2f}">'
            f'<title>Oh My Boot</title>{body}</svg>\n')


def boot_menu_svg(font):
    """640x480 background for `console --picture`; menu text is drawn by iPXE below y=88."""
    body, _ = lockup_parts(font, 40, TILE_DARK, RING, "#FFFFFF", ACCENT, 32, 24)
    return (f'<svg xmlns="http://www.w3.org/2000/svg" width="640" height="480" viewBox="0 0 640 480">'
            f'<rect width="640" height="480" fill="{MENU_BG}"/>{body}</svg>\n')


def write(path, text):
    os.makedirs(os.path.dirname(path), exist_ok=True)
    with open(path, "w", encoding="utf-8") as f:
        f.write(text)


def main():
    font_path, out = sys.argv[1], sys.argv[2]
    font = load_font(font_path)
    svg = {
        "mark/omb-mark.svg": mark_svg("main", TILE, RING, ACCENT, 32),
        "mark/omb-mark-16.svg": mark_svg("small", TILE, RING, ACCENT, 16),
        "mark/omb-mark-plain.svg": mark_svg("main", None, INK, ACCENT, 32),
        "mark/omb-mark-mono.svg": mark_svg("main", None, "currentColor", "currentColor", 32),
        "logo/omb-wordmark-light.svg": wordmark_svg(font, INK, ACCENT),
        "logo/omb-wordmark-dark.svg": wordmark_svg(font, RING, ACCENT),
        "logo/omb-wordmark-mono.svg": wordmark_svg(font, "currentColor", "currentColor"),
        "logo/omb-logo-light.svg": lockup_svg(font, TILE, RING, INK, ACCENT),
        "logo/omb-logo-dark.svg": lockup_svg(font, TILE_DARK, RING, RING, ACCENT),
        "logo/omb-logo-mono.svg": lockup_svg(font, None, "currentColor", "currentColor", "currentColor"),
        "favicon/favicon.svg": mark_svg("small", TILE, RING, ACCENT, 32),
        "boot/omb-menu-640x480.svg": boot_menu_svg(font),
    }
    for rel, text in svg.items():
        write(os.path.join(out, rel), text)
    rasters(out)


def rasters(out):
    from PIL import Image
    from playwright.sync_api import sync_playwright

    def render(svg_path, w, h):
        with open(svg_path, encoding="utf-8") as f:
            src = f.read()
        page.set_viewport_size({"width": w, "height": h})
        page.set_content(f'<html><body style="margin:0;background:transparent">'
                         f'<img src="data:image/svg+xml;base64,{base64.b64encode(src.encode()).decode()}" '
                         f'width="{w}" height="{h}" style="display:block"></body></html>')
        page.wait_for_load_state("networkidle")
        return Image.open(io.BytesIO(page.screenshot(omit_background=True))).convert("RGBA")

    with sync_playwright() as p:
        browser = p.chromium.launch()
        page = browser.new_page(device_scale_factor=1)
        fav = os.path.join(out, "favicon")
        main32 = os.path.join(out, "mark/omb-mark.svg")
        small = os.path.join(out, "mark/omb-mark-16.svg")
        icons = [render(small, 16, 16), render(main32, 32, 32), render(main32, 48, 48)]
        # Pillow takes the largest image as the base and exact-size frames from append_images.
        icons[2].save(os.path.join(fav, "favicon.ico"), sizes=[(16, 16), (32, 32), (48, 48)],
                      append_images=icons[:2])
        for n in (192, 512):
            render(main32, n, n).save(os.path.join(fav, f"icon-{n}.png"), optimize=True)
        # Apple touch icon: full square plate, the system rounds the corners.
        sq = os.path.join(out, "favicon/_apple.svg")
        write(sq, mark_svg("main", TILE, RING, ACCENT, 180, square=True))
        render(sq, 180, 180).convert("RGB").save(os.path.join(fav, "apple-touch-icon.png"), optimize=True)
        os.remove(sq)
        # Boot menu: 8-bit RGB, no alpha, no interlace (iPXE png.c reads all of these).
        menu = render(os.path.join(out, "boot/omb-menu-640x480.svg"), 640, 480).convert("RGB")
        menu.save(os.path.join(out, "boot/omb-menu-640x480.png"), optimize=True)
        browser.close()


if __name__ == "__main__":
    main()
