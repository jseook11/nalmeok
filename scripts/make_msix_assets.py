# Make the MSIX logo PNGs from the 1024 master icon (assets/nalmeok.png).
# Usage: uv run --no-project --with pillow python -I scripts/make_msix_assets.py \
#   assets/nalmeok.png packaging/windows/Assets
# No scale qualifiers: the package has no PRI index, so only the base names are used.
import os
import sys
from PIL import Image

src_path, out_dir = sys.argv[1:3]

SIZES = {
    "StoreLogo.png": 100,
    "Square44x44Logo.png": 88,
    "Square150x150Logo.png": 300,
}

# Same tight crop as the Windows .ico in make_icon_master.py (macOS grid
# padding reads too small on the Start menu and taskbar).
PAD, BODY = 100, 824
src = Image.open(src_path).convert("RGBA").crop((PAD - 16, PAD - 16, PAD + BODY + 16, PAD + BODY + 28))
os.makedirs(out_dir, exist_ok=True)
for name, size in SIZES.items():
    src.resize((size, size), Image.LANCZOS).save(os.path.join(out_dir, name))
    print(os.path.join(out_dir, name))
