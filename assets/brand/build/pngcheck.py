import zlib, struct

def read_png(path):
    with open(path, 'rb') as f:
        data = f.read()
    assert data[:8] == b'\x89PNG\r\n\x1a\n'
    pos = 8
    width = height = bitdepth = colortype = None
    idat = b''
    while pos < len(data):
        length = struct.unpack('>I', data[pos:pos+4])[0]
        ctype = data[pos+4:pos+8]
        chunk = data[pos+8:pos+8+length]
        if ctype == b'IHDR':
            width, height, bitdepth, colortype = struct.unpack('>IIBB', chunk[:10])
        elif ctype == b'IDAT':
            idat += chunk
        pos += 8 + length + 4
    raw = zlib.decompress(idat)
    if colortype == 6:
        bpp = 4
    elif colortype == 2:
        bpp = 3
    else:
        raise ValueError(f"unsupported colortype {colortype} bitdepth {bitdepth}")
    stride = width * bpp
    prev = bytearray(stride)
    out_rows = []
    off = 0
    for row in range(height):
        filtertype = raw[off]; off += 1
        line = bytearray(raw[off:off+stride]); off += stride
        if filtertype == 1:
            for i in range(stride):
                a = line[i-bpp] if i >= bpp else 0
                line[i] = (line[i] + a) & 0xff
        elif filtertype == 2:
            for i in range(stride):
                line[i] = (line[i] + prev[i]) & 0xff
        elif filtertype == 3:
            for i in range(stride):
                a = line[i-bpp] if i >= bpp else 0
                b = prev[i]
                line[i] = (line[i] + ((a+b)//2)) & 0xff
        elif filtertype == 4:
            for i in range(stride):
                a = line[i-bpp] if i >= bpp else 0
                b = prev[i]
                c = prev[i-bpp] if i >= bpp else 0
                p = a+b-c
                pa,pb,pc = abs(p-a),abs(p-b),abs(p-c)
                pr = a if (pa<=pb and pa<=pc) else (b if pb<=pc else c)
                line[i] = (line[i] + pr) & 0xff
        out_rows.append(line)
        prev = line
    return width, height, bpp, out_rows

def get_pixel(path, x, y):
    w, h, bpp, rows = read_png(path)
    row = rows[y]
    i = x*bpp
    px = tuple(row[i:i+bpp])
    if bpp == 3:
        px = px + (255,)
    return px

def scan_alpha_stats(path):
    """Return (has_transparent, has_opaque, any_grey_or_white_leak)"""
    w, h, bpp, rows = read_png(path)
    if bpp != 4:
        return (False, True, False)
    has_t = has_o = leak = False
    for row in rows:
        for i in range(0, len(row), 4):
            r,g,b,a = row[i:i+4]
            if a == 0:
                has_t = True
            else:
                has_o = True
                if a > 0 and (r>60 or g>60 or b>60):
                    # non-black content present with alpha - flag if r/g/b differ a lot suggesting grey/white leak
                    if abs(int(r)-int(g)) < 10 and abs(int(g)-int(b)) < 10 and r > 60:
                        leak = True
    return (has_t, has_o, leak)

if __name__ == "__main__":
    import sys
    p = sys.argv[1]
    x = int(sys.argv[2]); y = int(sys.argv[3])
    print(get_pixel(p, x, y))
