import sys, os
sys.path.insert(0, ".")
from render import render

OUT = "../exports/menubar"

def wrapper_html(size):
    scale = size / 1024.0
    return f"""<!DOCTYPE html>
<html><head><style>
html,body {{ margin:0; padding:0; background:transparent; }}
canvas {{ display:block; }}
</style></head>
<body>
<canvas id="c" width="{size}" height="{size}"></canvas>
<script>
const scale = {scale};
const ctx = document.getElementById('c').getContext('2d');

function pt(x, y) {{ return [x*scale, y*scale]; }}

const silhouette = [
  pt(300,140), pt(724,140), pt(964,340), pt(512,920), pt(60,340)
];

// fill silhouette black
ctx.fillStyle = '#000000';
ctx.beginPath();
ctx.moveTo(silhouette[0][0], silhouette[0][1]);
for (let i=1;i<silhouette.length;i++) ctx.lineTo(silhouette[i][0], silhouette[i][1]);
ctx.closePath();
ctx.fill();

// erase seam cut lines
ctx.globalCompositeOperation = 'destination-out';
ctx.lineWidth = 52*scale;
ctx.lineCap = 'square';
const seams = [
  [pt(60,340), pt(964,340)],
  [pt(300,140), pt(512,920)],
  [pt(724,140), pt(512,920)],
];
for (const [a,b] of seams) {{
  ctx.beginPath();
  ctx.moveTo(a[0], a[1]);
  ctx.lineTo(b[0], b[1]);
  ctx.stroke();
}}

// draw node circles back on top
ctx.globalCompositeOperation = 'source-over';
ctx.fillStyle = '#000000';
const nodes = [pt(300,140), pt(724,140), pt(964,340), pt(512,920), pt(60,340)];
const r = 76*scale;
for (const [cx,cy] of nodes) {{
  ctx.beginPath();
  ctx.arc(cx, cy, r, 0, Math.PI*2);
  ctx.fill();
}}
</script>
</body></html>
"""

sizes = {
    "template-16.png": 16,
    "template-32.png": 32,
    "template-18.png": 18,
    "template-36.png": 36,
}

for name, size in sizes.items():
    html = wrapper_html(size)
    out = os.path.join(OUT, name)
    render(html, size, size, out, transparent=True)
    print("rendered", name, size)
