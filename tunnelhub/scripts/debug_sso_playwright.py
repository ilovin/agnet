#!/usr/bin/env python3
"""
用 Playwright 自动获取 NIO OpenSSO authorization code / access token。
适用于 localhost redirect_uri 未注册、浏览器不自动跳转的场景。
用法:
    python3 debug_sso_playwright.py
"""

import json
import sys
import urllib.request
import urllib.parse
import urllib.error

AUTHORIZE_URL = "https://signin.nio.com/oauth2/authorize"
TOKEN_URL = "https://signin.nio.com/oauth2/accessToken"
PROFILE_URL = "https://signin.nio.com/oauth2/profile"
CLIENT_ID = "2000810"
REDIRECT_URI = "http://localhost:8384/callback"


def get_access_token(code):
    data = urllib.parse.urlencode({
        "grant_type": "authorization_code",
        "code": code,
        "redirect_uri": REDIRECT_URI,
        "client_id": CLIENT_ID,
    }).encode("utf-8")
    req = urllib.request.Request(TOKEN_URL, data=data, headers={"Content-Type": "application/x-www-form-urlencoded"}, method="POST")
    try:
        with urllib.request.urlopen(req, timeout=10) as resp:
            return json.loads(resp.read().decode("utf-8"))
    except urllib.error.HTTPError as e:
        print(f"[Token Error] {e.code}: {e.read().decode('utf-8')}")
        return None
    except Exception as e:
        print(f"[Token Error] {e}")
        return None


def get_profile(token):
    req = urllib.request.Request(
        PROFILE_URL,
        data=urllib.parse.urlencode({"accessToken": token}).encode("utf-8"),
        headers={"Content-Type": "application/x-www-form-urlencoded"},
        method="POST"
    )
    try:
        with urllib.request.urlopen(req, timeout=10) as resp:
            return json.loads(resp.read().decode("utf-8"))
    except urllib.error.HTTPError as e:
        print(f"[Profile Error] {e.code}: {e.read().decode('utf-8')}")
        return None
    except Exception as e:
        print(f"[Profile Error] {e}")
        return None


def main():
    try:
        from playwright.sync_api import sync_playwright
    except ImportError:
        print("请先安装 playwright:  pip install playwright  &&  python -m playwright install chromium")
        sys.exit(1)

    auth_url = (
        f"{AUTHORIZE_URL}?"
        f"response_type=code&"
        f"client_id={urllib.parse.quote(CLIENT_ID)}&"
        f"redirect_uri={urllib.parse.quote(REDIRECT_URI)}"
    )

    print("=" * 60)
    print("正在启动 Playwright Chromium...")
    print("请在弹出的浏览器窗口中完成 SSO 登录")
    print("=" * 60)

    code = None
    with sync_playwright() as p:
        browser = p.chromium.launch(headless=False)
        context = browser.new_context()
        page = context.new_page()

        # 监听所有 response，尝试从 302 Location 里提取 code
        def handle_response(response):
            nonlocal code
            loc = response.headers.get("location", "")
            if "localhost:8384/callback" in loc and "code=" in loc:
                parsed = urllib.parse.urlparse(loc)
                qs = urllib.parse.parse_qs(parsed.query)
                if "code" in qs and code is None:
                    code = qs["code"][0]
                    print(f"\n[Intercepted] 从 302 Location 提取到 code: {code[:20]}...")

        page.on("response", handle_response)

        page.goto(auth_url)

        # 轮询检查当前 URL 是否已经变成 callback
        for _ in range(300):  # 最多等 5 分钟
            if code:
                break
            current = page.url
            if "localhost:8384/callback" in current:
                parsed = urllib.parse.urlparse(current)
                qs = urllib.parse.parse_qs(parsed.query)
                if "code" in qs:
                    code = qs["code"][0]
                    print(f"\n[URL Match] 提取到 code: {code[:20]}...")
                    break
            page.wait_for_timeout(1000)

        browser.close()

    if not code:
        print("\n未能在浏览器中找到 authorization code。可能原因:")
        print("  1. 登录未完成")
        print("  2. SSO 未发起任何到 localhost 的重定向")
        print("  3. 需要联系管理员在 SSO/Sentry 后台添加 redirect_uri")
        fallback = input("\n如果浏览器地址栏有 code，请手动粘贴整个 callback URL 或 code: ").strip()
        if "code=" in fallback:
            parsed = urllib.parse.urlparse(fallback)
            qs = urllib.parse.parse_qs(parsed.query)
            if "code" in qs:
                code = qs["code"][0]
        else:
            code = fallback

    if not code:
        print("没有 code，退出")
        return

    print(f"\n[OK] code: {code[:20]}...")
    print("\n正在换取 accessToken...")
    token_resp = get_access_token(code)
    if not token_resp:
        return

    print("\n[Token Response]")
    print(json.dumps(token_resp, indent=2, ensure_ascii=False))

    access_token = token_resp.get("access_token") or token_resp.get("accessToken")
    if not access_token:
        print("\n未找到 access_token")
        return

    print(f"\n[OK] access_token: {access_token[:40]}...")
    print("\n正在验证 profile 接口...")
    profile = get_profile(access_token)
    if profile:
        print("\n[Profile Response]")
        print(json.dumps(profile, indent=2, ensure_ascii=False))
    else:
        print("\nProfile 接口调用失败")


if __name__ == "__main__":
    main()
