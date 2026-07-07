import subprocess, sys, os

CHROME = "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome"
WORK = "."

def render(html, width, height, out_png, transparent=True):
    os.makedirs(WORK, exist_ok=True)
    wrapper_path = os.path.join(WORK, "wrapper_" + os.path.basename(out_png) + ".html")
    with open(wrapper_path, "w") as f:
        f.write(html)
    os.makedirs(os.path.dirname(out_png), exist_ok=True)
    cmd = [
        CHROME, "--headless", "--disable-gpu",
        f"--screenshot={out_png}",
        f"--window-size={width},{height}",
        "--force-device-scale-factor=1",
    ]
    if transparent:
        cmd.append("--default-background-color=00000000")
    cmd.append(f"file://{wrapper_path}")
    r = subprocess.run(cmd, capture_output=True, text=True)
    if r.returncode != 0:
        print("FAILED", out_png, r.stdout, r.stderr)
        sys.exit(1)
    return out_png

if __name__ == "__main__":
    print("render module ready")
