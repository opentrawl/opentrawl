import sys, os
sys.path.insert(0, ".")
from render import render

MARKS_DIR = "../src"
ICONSET = "../exports/icns/OpenTrawl.iconset"

with open(os.path.join(MARKS_DIR, "n1v5-gemtwork-wrap.svg")) as f:
    big_svg = f.read().replace("#FNT", "#c9c9c9")

with open(os.path.join(MARKS_DIR, "n1v5s-small.svg")) as f:
    small_svg = f.read()

def wrapper_html(svg_content, canvas_size):
    rr = canvas_size * 824 / 1024
    radius = canvas_size * 185 / 1024
    mark = rr * 0.86
    offset = (canvas_size - rr) / 2
    mark_offset = (rr - mark) / 2
    return f"""<!DOCTYPE html>
<html><head><style>
html,body {{ margin:0; padding:0; background:transparent; }}
#outer {{ position:relative; width:{canvas_size}px; height:{canvas_size}px; background:transparent; }}
#rr {{ position:absolute; left:{offset}px; top:{offset}px; width:{rr}px; height:{rr}px;
       border-radius:{radius}px; overflow:hidden;
       background:linear-gradient(#ffffff, #e8e8e8); }}
#mark {{ position:absolute; left:{mark_offset}px; top:{mark_offset}px; width:{mark}px; height:{mark}px; }}
#mark svg {{ width:100%; height:100%; display:block; }}
</style></head>
<body>
<div id="outer">
  <div id="rr">
    <div id="mark">{svg_content}</div>
  </div>
</div>
</body></html>
"""

# (filename, real pixel size)
specs = [
    ("icon_16x16.png", 16),
    ("icon_16x16@2x.png", 32),
    ("icon_32x32.png", 32),
    ("icon_32x32@2x.png", 64),
    ("icon_128x128.png", 128),
    ("icon_128x128@2x.png", 256),
    ("icon_256x256.png", 256),
    ("icon_256x256@2x.png", 512),
    ("icon_512x512.png", 512),
    ("icon_512x512@2x.png", 1024),
]

for name, px in specs:
    svg = big_svg if px >= 128 else small_svg
    html = wrapper_html(svg, px)
    out = os.path.join(ICONSET, name)
    render(html, px, px, out, transparent=True)
    print("rendered", name, px)
