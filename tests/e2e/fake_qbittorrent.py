#!/usr/bin/env python3
"""A minimal qBittorrent WebUI API v2 stand-in, for end-to-end testing.

Serves one uncategorised completed movie torrent and one categorised one,
so the orphan predicate has something to both accept and reject.
"""
import json, os, sys
from http.server import BaseHTTPRequestHandler, HTTPServer

DL = sys.argv[2]

TORRENTS = [
    {
        "hash": "aaaa1111", "infohash_v1": "aaaa1111", "infohash_v2": "",
        "name": "The.Matrix.1999.1080p.BluRay.x264-AMIABLE",
        "category": "", "tags": "", "save_path": DL,
        "content_path": DL + "/The.Matrix.1999.1080p.BluRay.x264-AMIABLE",
        "state": "stalledUP", "progress": 1.0,
        "size": 24, "total_size": 24, "amount_left": 0,
        "completion_on": 1700000000,
    },
    {
        "hash": "bbbb2222", "infohash_v1": "bbbb2222", "infohash_v2": "",
        "name": "Some.Show.S01E01.1080p.WEB-DL.x264-GRP",
        "category": "tv-sonarr", "tags": "", "save_path": DL,
        "content_path": DL + "/Some.Show.S01E01.1080p.WEB-DL.x264-GRP",
        "state": "uploading", "progress": 1.0,
        "size": 12, "total_size": 12, "amount_left": 0,
        "completion_on": 1700000000,
    },
]

FILES = {
    "aaaa1111": [
        {"name": "The.Matrix.1999.1080p.BluRay.x264-AMIABLE/the.matrix.1999.mkv",
         "size": 24, "priority": 1},
    ],
    "bbbb2222": [
        {"name": "Some.Show.S01E01.1080p.WEB-DL.x264-GRP/ep.mkv", "size": 12, "priority": 1},
    ],
}


class H(BaseHTTPRequestHandler):
    def log_message(self, *a):
        pass

    def _send(self, body, ctype="application/json"):
        b = body.encode() if isinstance(body, str) else body
        self.send_response(200)
        self.send_header("Content-Type", ctype)
        self.send_header("Content-Length", str(len(b)))
        self.end_headers()
        self.wfile.write(b)

    def do_GET(self):
        p = self.path.split("?")[0]
        if p == "/api/v2/app/version":
            return self._send("v5.0.3", "text/plain")
        if p == "/api/v2/app/webapiVersion":
            return self._send("2.11.2", "text/plain")
        if p == "/api/v2/torrents/categories":
            return self._send(json.dumps({"tv-sonarr": {"name": "tv-sonarr", "savePath": ""}}))
        if p == "/api/v2/torrents/tags":
            return self._send(json.dumps([]))
        if p == "/api/v2/torrents/info":
            return self._send(json.dumps(TORRENTS))
        if p == "/api/v2/torrents/files":
            q = self.path.split("hash=")[-1]
            return self._send(json.dumps(FILES.get(q, [])))
        self.send_error(404)

    def do_POST(self):
        p = self.path.split("?")[0]
        if p == "/api/v2/auth/login":
            self.send_response(200)
            self.send_header("Set-Cookie", "SID=fake; path=/")
            self.send_header("Content-Length", "0")
            self.end_headers()
            return
        if p == "/api/v2/torrents/addTags":
            n = int(self.headers.get("Content-Length", 0))
            self.rfile.read(n)
            return self._send("", "text/plain")
        self.send_error(404)


HTTPServer(("127.0.0.1", int(sys.argv[1])), H).serve_forever()
