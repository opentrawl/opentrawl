import os, re, sys
sys.path.insert(0, ".")
import render as chrome_renderer
from render import render

# render.py writes its wrapper next to WORK and hands Chrome file://{path};
# a relative WORK produces file://./wrapper.html, which Chrome rejects.
chrome_renderer.WORK = os.path.abspath(".")

# The social card the website serves as og:image and twitter:image.
# Its words come from website/copy.txt, so the card cannot drift from the page.

REPOSITORY_ROOT = os.path.abspath("../../..")
COPY_FILE = os.path.join(REPOSITORY_ROOT, "website", "copy.txt")
MARK_FILE = "../exports/web/mark.svg"
SOCIAL_EXPORT = "../exports/social/card.png"
WEBSITE_COPY_OF_CARD = os.path.join(REPOSITORY_ROOT, "website", "card.png")

CARD_WIDTH, CARD_HEIGHT = 1200, 630


def read_copy_slots(path):
    slots, current = {}, None
    for line in open(path, encoding="utf-8"):
        if line.startswith("#"):
            continue
        named = re.match(r"^\[([\w.]+)\]\s*$", line)
        if named:
            current = named.group(1)
            slots[current] = []
        elif current and line.strip():
            slots[current].append(line.strip())
    return {name: " ".join(lines) for name, lines in slots.items()}


copy = read_copy_slots(COPY_FILE)
first_source = copy["hero.cycle"].split(",")[0].strip()
mark = open(MARK_FILE, encoding="utf-8").read()

html = f"""<!DOCTYPE html>
<html><head><meta charset="utf-8"><style>
  * {{ box-sizing: border-box; }}
  html, body {{ margin: 0; padding: 0; overflow: hidden; }}
  body {{
    width: {CARD_WIDTH}px; height: {CARD_HEIGHT}px; background: #ffffff; color: #101010;
    font-family: "Helvetica Neue", Helvetica, Arial, sans-serif;
    -webkit-font-smoothing: antialiased;
    padding: 56px 64px; display: flex; flex-direction: column; justify-content: space-between;
  }}
  .brand {{ display: flex; align-items: center; gap: 12px; }}
  .brand svg {{ width: 44px; height: 44px; display: block; }}
  .wordmark {{ font-size: 34px; font-weight: 700; letter-spacing: -.01em; text-transform: lowercase; }}
  .wordmark span {{ color: #e63323; }}
  h1 {{ font-size: 52px; font-weight: 700; letter-spacing: -.03em; line-height: 1.1; margin: 0; }}
  h1 span {{ color: #e63323; }}
  .sources {{ border-top: 2px solid #101010; padding-top: 16px; font-size: 21px; line-height: 1.5; }}
  .sources b {{ font-weight: 700; }}
  .sources .soon {{ color: #6f6f6f; }}
</style></head>
<body>
  <div class="brand">{mark}<div class="wordmark">open<span>trawl</span></div></div>
  <h1>{copy["hero.line1"]} <span>{first_source}</span><br>{copy["hero.line2"]}</h1>
  <p class="sources">{copy["sources.today"].replace("**", "")}<br>
    <span class="soon">{copy["sources.soon"].replace("**", "")}</span></p>
</body></html>
"""

render(html, CARD_WIDTH, CARD_HEIGHT, SOCIAL_EXPORT, transparent=False)
os.remove(os.path.join(chrome_renderer.WORK, "wrapper_card.png.html"))
with open(SOCIAL_EXPORT, "rb") as source, open(WEBSITE_COPY_OF_CARD, "wb") as destination:
    destination.write(source.read())
print("rendered", SOCIAL_EXPORT, "and copied to website/card.png")
