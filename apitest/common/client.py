"""HTTP 客户端：Bearer 令牌、JSON 收发、SSE 解帧。

只依赖标准库，避免为了跑测试去装 requests。
"""

import json
import time
import urllib.error
import urllib.request

from . import env


class Response:
    """一次 HTTP 调用的结果。"""

    def __init__(self, status, headers, body, elapsed_ms):
        self.status = status
        self.headers = headers
        self.body = body
        self.elapsed_ms = elapsed_ms

    @property
    def json(self):
        try:
            return json.loads(self.body) if self.body else None
        except ValueError:
            return None

    @property
    def text(self):
        return (self.body or b"").decode("utf-8", "replace")

    def header(self, name):
        for key, value in self.headers.items():
            if key.lower() == name.lower():
                return value
        return None

    def __repr__(self):
        return f"<Response {self.status} {len(self.body or b'')}B>"


class Client:
    """一个持令牌的会话。未登录时 token 为 None。"""

    def __init__(self, base_url=None, token=None):
        self.base_url = (base_url or env.BASE_URL).rstrip("/")
        self.token = token

    # --- 基础请求 ---------------------------------------------------------

    def request(self, method, path, json_body=None, raw=None, headers=None,
                use_token=True, timeout=180):
        if raw is not None:
            body = raw
        elif json_body is not None:
            body = json.dumps(json_body).encode()
        else:
            body = None

        hdrs = dict(headers or {})
        if body is not None and "Content-Type" not in hdrs:
            hdrs["Content-Type"] = "application/json"
        if use_token and self.token:
            hdrs["Authorization"] = "Bearer " + self.token

        req = urllib.request.Request(self.base_url + path, data=body, headers=hdrs,
                                     method=method.upper())
        started = time.time()
        try:
            resp = urllib.request.urlopen(req, timeout=timeout)
            payload = resp.read()
            elapsed = (time.time() - started) * 1000
            return Response(resp.status, dict(resp.headers), payload, elapsed)
        except urllib.error.HTTPError as exc:
            payload = exc.read()
            elapsed = (time.time() - started) * 1000
            return Response(exc.code, dict(exc.headers), payload, elapsed)
        except Exception as exc:  # noqa: BLE001
            elapsed = (time.time() - started) * 1000
            return Response(None, {}, f"{type(exc).__name__}: {exc}".encode(), elapsed)

    def get(self, path, **kwargs):
        return self.request("GET", path, **kwargs)

    def post(self, path, **kwargs):
        return self.request("POST", path, **kwargs)

    def put(self, path, **kwargs):
        return self.request("PUT", path, **kwargs)

    def delete(self, path, **kwargs):
        return self.request("DELETE", path, **kwargs)

    # --- 登录 -------------------------------------------------------------

    def login(self, username, password, send_old_token=False):
        """登录并保存令牌。

        send_old_token 为真时把当前令牌一并带上——规约要求「携带未过期旧 Bearer
        再次登录」时先吊销旧 jti，这样测才能覆盖到。
        """
        resp = self.post("/api/login", json_body={"username": username, "password": password},
                         use_token=send_old_token)
        info = resp.json or {}
        if isinstance(info, dict) and info.get("token"):
            self.token = info["token"]
        return resp

    # --- SSE --------------------------------------------------------------

    def sse(self, path, json_body, timeout=180):
        """POST 一个 SSE 接口，逐帧解析，返回 (status, headers, events)。

        events 里每项是 {"t": 相对到达秒数, "event": 事件名, "data": 解析后的载荷}。
        """
        headers = {"Content-Type": "application/json", "Accept": "text/event-stream"}
        if self.token:
            headers["Authorization"] = "Bearer " + self.token
        req = urllib.request.Request(
            self.base_url + path, data=json.dumps(json_body).encode(),
            headers=headers, method="POST")

        started = time.time()
        try:
            resp = urllib.request.urlopen(req, timeout=timeout)
        except urllib.error.HTTPError as exc:
            return exc.code, dict(exc.headers), []
        except Exception as exc:  # noqa: BLE001
            return None, {"error": str(exc)}, []

        events = []
        buf = b""
        while True:
            chunk = resp.read(1)
            if not chunk:
                break
            buf += chunk
            while b"\n\n" in buf:
                frame, buf = buf.split(b"\n\n", 1)
                name, data = None, None
                for line in frame.decode("utf-8", "replace").split("\n"):
                    if line.startswith("event: "):
                        name = line[7:].strip()
                    elif line.startswith("data: "):
                        data = line[6:]
                if not name:
                    continue
                try:
                    payload = json.loads(data) if data else None
                except ValueError:
                    payload = data
                events.append({"t": round(time.time() - started, 3),
                               "event": name, "data": payload})
        return resp.status, dict(resp.headers), events
