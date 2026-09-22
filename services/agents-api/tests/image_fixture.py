"""Small deterministic PNG fixture shared by real image acceptance scripts."""
import base64
import struct
import zlib


def picture(names):
    colors = {"red": (255, 0, 0), "blue": (0, 0, 255), "green": (0, 170, 0), "yellow": (255, 255, 0)}
    rows = b"".join(b"\0" + b"".join(bytes(colors[n]) * 100 for n in names) for _ in range(140))

    def chunk(kind, value):
        return struct.pack(">I", len(value)) + kind + value + struct.pack(">I", zlib.crc32(kind + value) & 0xffffffff)

    image = b"\x89PNG\r\n\x1a\n" + chunk(b"IHDR", struct.pack(">IIBBBBB", 400, 140, 8, 2, 0, 0, 0)) + chunk(b"IDAT", zlib.compress(rows)) + chunk(b"IEND", b"")
    return "data:image/png;base64," + base64.b64encode(image).decode()
