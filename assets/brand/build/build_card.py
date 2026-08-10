import base64, os, re, sys
sys.path.insert(0, ".")
import render as chrome_renderer
from render import render

# The social card the website serves as og:image and twitter:image.
# Its words come from website/copy.txt, so the card cannot drift from the page.
# render.py hands Chrome file://{path}; a relative WORK makes that invalid.
chrome_renderer.WORK = os.path.abspath(".")

REPOSITORY_ROOT = os.path.abspath("../../..")
COPY_FILE = os.path.join(REPOSITORY_ROOT, "website", "copy.txt")
# brand rule: below 128px always use the small mark, the full one turns to mud
MARK_FILE = "../src/mark-small.svg"
APP_ICON_DIRECTORY = "../app-icons"
SOCIAL_EXPORT = "../exports/social/card.png"
WEBSITE_COPY_OF_CARD = os.path.join(REPOSITORY_ROOT, "website", "card.png")

CARD_WIDTH, CARD_HEIGHT = 1200, 630

# in the order the app lists them; the last three are not shipping yet
APP_ICONS = [
    "imessage", "whatsapp", "telegram", "notes", "contacts",
    "calendar", "gmail", "twitter", "photos",
]
# App Store artwork is a plain square, so it needs the squircle the Mac icons already have
SQUARE_ARTWORK = {"gmail", "twitter"}


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


def inline_png(name):
    path = os.path.join(APP_ICON_DIRECTORY, name + ".png")
    with open(path, "rb") as handle:
        return "data:image/png;base64," + base64.b64encode(handle.read()).decode("ascii")


def icon_tag(name):
    classes = "icon square" if name in SQUARE_ARTWORK else "icon"
    return f'<img class="{classes}" src="{inline_png(name)}" alt="">'


copy = read_copy_slots(COPY_FILE)
mark = open(MARK_FILE, encoding="utf-8").read().replace("#FNT", "#c9c9c9").replace("#INK", "#101010")
icons = "".join(icon_tag(name) for name in APP_ICONS)

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
  .brand {{ display: flex; align-items: center; gap: 16px; }}
  /* our mark is never smaller than the app icons it sits above */
  .brand svg {{ width: 112px; height: 112px; display: block; }}
  .wordmark {{ font-size: 62px; font-weight: 700; letter-spacing: -.01em; text-transform: lowercase; }}
  .wordmark span {{ color: #e63323; }}
  h1 {{ font-size: 72px; font-weight: 700; letter-spacing: -.03em; line-height: 1.06; margin: 0; }}
  h1 .second {{ display: block; color: #e63323; }}
  .apps {{ display: flex; justify-content: space-between; align-items: center;
           border-top: 2px solid #101010; padding-top: 26px; }}
  .icon {{ width: 104px; height: 104px; display: block; }}
  .icon.square {{ border-radius: 24px; }}
</style></head>
<body>
  <div class="brand">{mark}<div class="wordmark">open<span>trawl</span></div></div>
  <h1>{copy["card.line.1"]}<br><span class="second">{copy["card.line.2"]}</span></h1>
  <div class="apps">{icons}</div>
</body></html>
"""

render(html, CARD_WIDTH, CARD_HEIGHT, SOCIAL_EXPORT, transparent=False)
os.remove(os.path.join(chrome_renderer.WORK, "wrapper_card.png.html"))
with open(SOCIAL_EXPORT, "rb") as source, open(WEBSITE_COPY_OF_CARD, "wb") as destination:
    destination.write(source.read())
print("rendered", SOCIAL_EXPORT, "and copied to website/card.png")
