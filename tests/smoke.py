#!/usr/bin/env python3
"""接口冒烟测试（只用标准库，不装任何依赖）。

检查的不是业务正确性，而是**契约形状**：信封是否统一、状态码对不对、
鉴权是否真的生效、错误码是不是预期的那个。业务正确性交给 Go 单测和契约快照。

用法：
    python3 tests/smoke.py                      # 默认 http://127.0.0.1:2333
    python3 tests/smoke.py --base https://blognext-go.yaria.top
    python3 tests/smoke.py --token "$TOKEN"     # 带上管理员 JWT，顺带测鉴权接口
    python3 tests/smoke.py --skip-write         # 只跑只读接口（默认就是）

退出码：0 全过；1 有用例失败。
"""
from __future__ import annotations

import argparse
import json
import sys
import urllib.error
import urllib.parse
import urllib.request

TIMEOUT = 15

# 只读接口：(method, path, query, 期望状态码, 期望 error.code 或 None)
# 返回 2xx 且 data 存在即视为形状正确；个别接口 data 可以为 null。
PUBLIC_READS = [
    ("GET", "/health", {}, 200, None),
    ("GET", "/posts/list", {"pageSize": 2}, 200, None),
    ("GET", "/posts/list", {"pageSize": 2, "countOnly": "true"}, 200, None),
    ("GET", "/posts/list", {"pageSize": "full"}, None, None),  # 可能 400(超上限) 或 200
    ("GET", "/posts/statistics", {}, 200, None),
    ("GET", "/posts/slugs", {}, 200, None),
    ("GET", "/tags/list", {}, 200, None),
    ("GET", "/categories/list", {}, 200, None),
    ("GET", "/archives/list", {}, 200, None),
    ("GET", "/links/list", {}, 200, None),
    ("GET", "/site/info", {}, 200, None),
    ("GET", "/speaks/list", {"pageSize": 2}, 200, None),
]

# 需要 slug 才能测，先从不带参数会 400 的角度确认参数校验生效
REQUIRE_PARAM = [
    ("GET", "/posts/detail", {}, 400, "validation.missing_field"),
    ("GET", "/posts/navigation", {}, 400, "validation.missing_field"),
    ("GET", "/posts/content", {}, 404, "not_found.route"),  # 这个接口已被删除，必须是 404
    ("GET", "/posts/summary", {}, 400, "validation.missing_field"),
    ("GET", "/links/applications/status", {}, 400, "validation.invalid_field"),
]

# 需要鉴权：不带 token 必须 401
PROTECTED = [
    ("GET", "/drafts/list", {}),
    ("GET", "/drafts/slugs", {}),
    ("GET", "/images/list", {}),
    ("GET", "/auth/verify", {}),
    ("GET", "/links/applications/list", {}),
    ("POST", "/posts/create", None),
    ("POST", "/maintenance/export", None),
]

# 预留 stub：必须固定 501
STUBS = [
    ("GET", "/posts/semantic", {"query": "你好"}),
]

# 必须完全不存在的路径
MISSING = ["/posts/nope", "/get/post/postsInfo", "/update/post/addPost"]


class Fail(Exception):
    pass


def request(base, method, path, query, token=None, body=None):
    url = base.rstrip("/") + path
    if query:
        url += "?" + urllib.parse.urlencode(query)
    data = None
    headers = {"Accept": "application/json"}
    if token:
        headers["Authorization"] = "Bearer " + token
    if body is not None:
        data = json.dumps(body).encode()
        headers["Content-Type"] = "application/json"
    req = urllib.request.Request(url, data=data, headers=headers, method=method)
    try:
        with urllib.request.urlopen(req, timeout=TIMEOUT) as resp:
            raw = resp.read()
            return resp.status, raw
    except urllib.error.HTTPError as e:
        return e.code, e.read()
    except Exception as e:  # 连不上
        raise Fail(f"{method} {path} 请求失败：{type(e).__name__}: {e}")


def decode(method, path, status, raw):
    ctype = ""
    try:
        obj = json.loads(raw.decode("utf-8"))
    except Exception:
        head = raw[:120].decode("utf-8", "replace")
        raise Fail(f"{method} {path} 返回 {status} 但不是 JSON：{head!r}")
    return obj


def check_envelope(method, path, obj, status, want_status, want_code):
    if want_status is not None and status != want_status:
        raise Fail(f"{method} {path} 期望状态码 {want_status}，实际 {status}")
    if "ok" not in obj:
        raise Fail(f"{method} {path} 响应缺少 ok 字段：{json.dumps(obj, ensure_ascii=False)[:200]}")
    if "requestId" not in obj:
        raise Fail(f"{method} {path} 响应缺少 requestId")
    if obj["ok"] is True:
        if want_code:
            raise Fail(f"{method} {path} 期望失败（{want_code}）却返回成功")
        if "data" not in obj:
            raise Fail(f"{method} {path} 成功响应缺少 data 键")
        if "error" in obj:
            raise Fail(f"{method} {path} 成功响应不该带 error")
    else:
        if "error" not in obj or not isinstance(obj["error"], dict):
            raise Fail(f"{method} {path} 失败响应缺少 error 对象")
        code = obj["error"].get("code", "")
        if not code:
            raise Fail(f"{method} {path} 失败响应缺少 error.code")
        if want_code and code != want_code:
            raise Fail(f"{method} {path} 期望错误码 {want_code}，实际 {code}")
        if "details" not in obj["error"]:
            raise Fail(f"{method} {path} 失败响应缺少 error.details 键")
    return obj


def main() -> int:
    ap = argparse.ArgumentParser()
    ap.add_argument("--base", default="http://127.0.0.1:2333")
    ap.add_argument("--token", default="")
    args = ap.parse_args()

    failures = []
    total = 0

    def run(name, fn):
        nonlocal total
        total += 1
        try:
            fn()
            print(f"  ✓ {name}")
        except Fail as e:
            failures.append(f"{name}: {e}")
            print(f"  ✗ {name}\n      {e}")

    print(f"冒烟测试 → {args.base}\n")

    print("[1] 公开只读接口")
    for method, path, query, want, want_code in PUBLIC_READS:
        def t(method=method, path=path, query=query, want=want, want_code=want_code):
            status, raw = request(args.base, method, path, query)
            check_envelope(method, path, decode(method, path, status, raw), status, want, want_code)
        run(f"{method} {path} {query or ''}", t)

    print("[2] 参数校验与已删除接口")
    for method, path, query, want, want_code in REQUIRE_PARAM:
        def t(method=method, path=path, query=query, want=want, want_code=want_code):
            status, raw = request(args.base, method, path, query)
            check_envelope(method, path, decode(method, path, status, raw), status, want, want_code)
        run(f"{method} {path} 无参数 → {want_code}", t)

    print("[3] 鉴权（不带 token 必须 401）")
    for method, path, body in PROTECTED:
        def t(method=method, path=path, body=body):
            status, raw = request(args.base, method, path, {}, body=body)
            obj = check_envelope(method, path, decode(method, path, status, raw), status, 401, None)
            code = obj["error"]["code"]
            if not code.startswith("auth."):
                raise Fail(f"{method} {path} 401 的错误码是 {code}，应为 auth.*")
        run(f"{method} {path}", t)

    print("[4] 预留 stub")
    for method, path, query in STUBS:
        def t(method=method, path=path, query=query):
            status, raw = request(args.base, method, path, query)
            check_envelope(method, path, decode(method, path, status, raw),
                           status, 501, "search.not_implemented")
        run(f"{method} {path} → 501", t)

    print("[5] 不存在的路径（含旧后端的路径，必须 404）")
    for path in MISSING:
        def t(path=path):
            status, raw = request(args.base, "GET", path, {})
            check_envelope("GET", path, decode("GET", path, status, raw),
                           status, 404, "not_found.route")
        run(f"GET {path} → 404", t)

    print("[6] CORS 预检")
    def preflight():
        req = urllib.request.Request(
            args.base.rstrip("/") + "/posts/list",
            headers={"Origin": "https://blog.yaria.top", "Access-Control-Request-Method": "GET"},
            method="OPTIONS",
        )
        try:
            with urllib.request.urlopen(req, timeout=TIMEOUT) as resp:
                allow = resp.headers.get("Access-Control-Allow-Origin", "")
                if not allow:
                    raise Fail("OPTIONS 响应缺少 Access-Control-Allow-Origin")
        except urllib.error.HTTPError as e:
            allow = e.headers.get("Access-Control-Allow-Origin", "")
            if not allow:
                raise Fail(f"OPTIONS 返回 {e.code} 且缺少 CORS 头")
    run("OPTIONS /posts/list", preflight)

    if args.token:
        print("[7] 带 token 的后台接口")
        def authed():
            status, raw = request(args.base, "GET", "/auth/verify", {}, token=args.token)
            check_envelope("GET", "/auth/verify", decode("GET", "/auth/verify", status, raw), status, 200, None)
        run("GET /auth/verify", authed)
        def authed_list():
            status, raw = request(args.base, "GET", "/drafts/list", {"pageSize": 1}, token=args.token)
            check_envelope("GET", "/drafts/list", decode("GET", "/drafts/list", status, raw), status, 200, None)
        run("GET /drafts/list", authed_list)

    print()
    if failures:
        print(f"失败 {len(failures)} / {total} ❌")
        for f in failures:
            print("  -", f)
        return 1
    print(f"全部通过 {total} / {total} ✅")
    return 0


if __name__ == "__main__":
    sys.exit(main())
