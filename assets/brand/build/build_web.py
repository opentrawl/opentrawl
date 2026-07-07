import sys, os
sys.path.insert(0, ".")
from render import render

MARKS_DIR = "../src"
OUT = "../exports/web"

with open(os.path.join(MARKS_DIR, "n1v5s-small.svg")) as f:
    small_svg = f.read()

def plain_wrapper(svg_content, size):
    return f"""<!DOCTYPE html>
<html><head><style>
html,body {{ margin:0; padding:0; background:transparent; }}
#w {{ width:{size}px; height:{size}px; }}
#w svg {{ width:100%; height:100%; display:block; }}
</style></head>
<body><div id="w">{svg_content}</div></body></html>
"""

# favicons: raw mark at native size on transparency
for name, size in [("favicon-16.png", 16), ("favicon-32.png", 32), ("favicon-48.png", 48)]:
    html = plain_wrapper(small_svg, size)
    out = os.path.join(OUT, name)
    render(html, size, size, out, transparent=True)
    print("rendered", name, size)

# apple-touch-icon.png: 180x180 rounded-rect style (section 1), mark = n1v5s-small
def rounded_wrapper_html(svg_content, canvas_size):
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

html = rounded_wrapper_html(small_svg, 180)
render(html, 180, 180, os.path.join(OUT, "apple-touch-icon.png"), transparent=True)
print("rendered apple-touch-icon.png 180")

# mark.svg: n1v5-gemtwork-wrap.svg, #FNT -> #c9c9c9, #INK -> #101010
with open(os.path.join(MARKS_DIR, "n1v5-gemtwork-wrap.svg")) as f:
    mark_svg = f.read()
mark_svg = mark_svg.replace("#FNT", "#c9c9c9").replace("#INK", "#101010")
with open(os.path.join(OUT, "mark.svg"), "w") as f:
    f.write(mark_svg)
print("wrote mark.svg")

# lockup variants
with open(os.path.join(MARKS_DIR, "lockup.svg")) as f:
    lockup_svg = f.read()

lockup_light = lockup_svg.replace("#INK", "#101010")
lockup_dark = lockup_svg.replace("#INK", "#ffffff")

with open(os.path.join(OUT, "lockup-light.svg"), "w") as f:
    f.write(lockup_light)
with open(os.path.join(OUT, "lockup-dark.svg"), "w") as f:
    f.write(lockup_dark)
print("wrote lockup-light.svg, lockup-dark.svg")

def lockup_wrapper(svg_content, w, h):
    return f"""<!DOCTYPE html>
<html><head><style>
html,body {{ margin:0; padding:0; background:transparent; }}
#w {{ width:{w}px; height:{h}px; }}
#w svg {{ width:100%; height:100%; display:block; }}
</style></head>
<body><div id="w">{svg_content}</div></body></html>
"""

render(lockup_wrapper(lockup_light, 1350, 512), 1350, 512, os.path.join(OUT, "lockup-light.png"), transparent=True)
render(lockup_wrapper(lockup_dark, 1350, 512), 1350, 512, os.path.join(OUT, "lockup-dark.png"), transparent=True)
print("rendered lockup-light.png, lockup-dark.png")
