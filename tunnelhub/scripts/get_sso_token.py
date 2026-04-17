#!/usr/bin/env python3
"""
本地自动获取 NIO OpenSSO access token 并验证 profile。
用法:
    python3 get_sso_token.py
然后复制打印的 URL 到浏览器登录，等待终端输出结果。
如果 localhost redirect_uri 未在 SSO 后台注册，脚本会提示手动输入 code。
"""

import http.server
import json
import socketserver
import threading
import urllib.request
import urllib.parse
import urllib.error

# NIO OpenSSO 配置 (prod 环境)
AUTHORIZE_URL = "https://signin.nio.com/oauth2/authorize"
TOKEN_URL = "https://signin.nio.com/oauth2/accessToken"
PROFILE_URL = "https://signin.nio.com/oauth2/profile"

# 在 Sentry/OpenSSO 申请到的 client_id
CLIENT_ID = "2000810"
REDIRECT_URI = "http://localhost:8384/callback"
PORT = 8384

code_event = threading.Event()
auth_code = None


class Handler(http.server.BaseHTTPRequestHandler):
    def do_GET(self):
        global auth_code
        parsed = urllib.parse.urlparse(self.path)
        qs = urllib.parse.parse_qs(parsed.query)

        if parsed.path == "/callback":
            if "code" in qs:
                auth_code = qs["code"][0]
                code_event.set()
                self.send_response(200)
                self.send_header("Content-Type", "text/html; charset=utf-8")
                self.end_headers()
                self.wfile.write(b"<h1>Login successful!</h1><p>You can close this tab and return to the terminal.</p>")
            elif "error" in qs:
                code_event.set()
                self.send_response(400)
                self.send_header("Content-Type", "text/html; charset=utf-8")
                self.end_headers()
                self.wfile.write(f"<h1>Error: {qs['error'][0]}</h1>".encode())
            else:
                self.send_response(400)
                self.end_headers()
            return

        self.send_response(404)
        self.end_headers()

    def log_message(self, format, *args):
        pass


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


def manual_fallback():
    global auth_code
    print("\n未能在浏览器中找到 authorization code。可能原因:")
    print("  1. 登录未完成")
    print("  2. SSO 未发起任何到 localhost 的重定向")
    print("  3. 需要联系管理员在 SSO/Sentry 后台添加 redirect_uri")
    fallback = input("\n如果浏览器地址栏有 code，请手动粘贴整个 callback URL 或 code: ").strip()
    if "code=" in fallback:
        parsed = urllib.parse.urlparse(fallback)
        qs = urllib.parse.parse_qs(parsed.query)
        if "code" in qs:
            auth_code = qs["code"][0]
    else:
        auth_code = fallback


def main():
    with socketserver.TCPServer(("", PORT), Handler) as httpd:
        t = threading.Thread(target=httpd.serve_forever)
        t.daemon = True
        t.start()

        auth_url = (
            f"{AUTHORIZE_URL}?"
            f"response_type=code&"
            f"client_id={urllib.parse.quote(CLIENT_ID)}&"
            f"redirect_uri={urllib.parse.quote(REDIRECT_URI)}"
        )
        print("=" * 60)
        print("Step 1: 请在浏览器中打开以下链接并完成登录")
        print("=" * 60)
        print(auth_url)
        print("=" * 60)
        print("等待回调...\n")

        code_event.wait(timeout=120)
        httpd.shutdown()

        if not auth_code:
            manual_fallback()

        if not auth_code:
            print("登录失败，未获取到 authorization code")
            return

        print(f"\n[OK] 获取到 code: {auth_code[:20]}...")
        print("\nStep 2: 正在用 code 换取 accessToken...")
        token_resp = get_access_token(auth_code)
        if not token_resp:
            return

        print("\n[Token Response]")
        print(json.dumps(token_resp, indent=2, ensure_ascii=False))

        access_token = token_resp.get("access_token") or token_resp.get("accessToken")
        if not access_token:
            print("\n未在响应中找到 access_token，请检查字段名")
            return

        print(f"\n[OK] access_token: {access_token[:40]}...")
        print("\nStep 3: 正在验证 profile 接口...")
        profile = get_profile(access_token)
        if profile:
            print("\n[Profile Response]")
            print(json.dumps(profile, indent=2, ensure_ascii=False))
            print("\n" + "=" * 60)
            print("环境变量导出（可直接复制给 agentgw 使用）:")
            print(f"export AGENTGW_TUNNEL_TOKEN='{access_token}'")
            print("=" * 60)
        else:
            print("\nProfile 接口调用失败")


if __name__ == "__main__":
    main()
