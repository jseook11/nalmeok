# Make the masked macOS-grid master icon (1024) and a multi-size .ico from the
# full-bleed artwork. Usage: master.py <art.png> <out-master.png> <out.ico>
import sys
from PIL import Image, ImageDraw, ImageFilter

art_path, out_png, out_ico = sys.argv[1:4]
S = 1024
BODY = 824
PAD = (S - BODY) // 2
R = 185
K = 4  # supersample

art = Image.open(art_path).convert("RGBA").resize((BODY * K, BODY * K), Image.LANCZOS)
mask = Image.new("L", art.size, 0)
ImageDraw.Draw(mask).rounded_rectangle([0, 0, BODY * K - 1, BODY * K - 1], radius=R * K, fill=255)
art.putalpha(mask)

# Faint light rim so the dark tile keeps its edge on dark Docks/menus.
rim = Image.new("RGBA", art.size, (0, 0, 0, 0))
w = 3 * K
ImageDraw.Draw(rim).rounded_rectangle([w // 2, w // 2, BODY * K - 1 - w // 2, BODY * K - 1 - w // 2],
                                      radius=R * K - w // 2, outline=(255, 255, 255, 34), width=w)
art = Image.alpha_composite(art, rim).resize((BODY, BODY), Image.LANCZOS)

# Soft drop shadow per macOS grid.
canvas = Image.new("RGBA", (S, S), (0, 0, 0, 0))
sh = Image.new("L", (S, S), 0)
ImageDraw.Draw(sh).rounded_rectangle([PAD, PAD + 12, PAD + BODY, PAD + BODY + 12], radius=R, fill=80)
sh = sh.filter(ImageFilter.GaussianBlur(14))
canvas.paste((0, 0, 0, 255), (0, 0), sh)
canvas.alpha_composite(art, (PAD, PAD))
canvas.save(out_png)

# Windows: tighter crop (less padding) reads better in the taskbar.
tight = canvas.crop((PAD - 16, PAD - 16, PAD + BODY + 16, PAD + BODY + 28)).resize((256, 256), Image.LANCZOS)
tight.save(out_ico, sizes=[(16, 16), (24, 24), (32, 32), (48, 48), (64, 64), (128, 128), (256, 256)])
print(out_png, out_ico)
