import sys, os
sys.path.insert(0, ".")
from render import render

MARKS_DIR = "../src"
OUT = "../exports/icon-composer"

def wrapper_html(svg_content, size):
    return f"""<!DOCTYPE html>
<html><head><style>
html,body {{ margin:0; padding:0; background:transparent; }}
#w {{ width:{size}px; height:{size}px; }}
#w svg {{ width:100%; height:100%; display:block; }}
</style></head>
<body><div id="w">{svg_content}</div></body></html>
"""

for name in ["layer-bg-wrap.svg", "layer-fg-gem.svg"]:
    with open(os.path.join(MARKS_DIR, name)) as f:
        svg = f.read()
    out_png = os.path.join(OUT, name.replace(".svg", ".png"))
    html = wrapper_html(svg, 1024)
    render(html, 1024, 1024, out_png, transparent=True)
    print("rendered", out_png)
