#!/usr/bin/env python3
"""
验证 NIO OpenSSO profile API 的调用方式。
用法:
    export ACCESS_TOKEN="你的token"
    python3 verify_sso_profile.py
或直接运行后按提示输入 token。
"""

import os
import json
import urllib.request
import urllib.parse

PROFILE_URL = "https://signin.nio.com/oauth2/profile"


def try_get_header(token):
    req = urllib.request.Request(
        PROFILE_URL,
        headers={"Authorization": f"Bearer {token}"},
        method="GET"
    )
    return do_request(req, "GET + Header Authorization: Bearer")


def try_get_query(token):
    url = f"{PROFILE_URL}?accessToken={urllib.parse.quote(token)}"
    req = urllib.request.Request(url, method="GET")
    return do_request(req, "GET + Query ?accessToken=")


def try_post_form(token):
    data = urllib.parse.urlencode({"accessToken": token}).encode("utf-8")
    req = urllib.request.Request(
        PROFILE_URL,
        data=data,
        headers={"Content-Type": "application/x-www-form-urlencoded"},
        method="POST"
    )
    return do_request(req, "POST + Form accessToken")


def try_post_json(token):
    data = json.dumps({"accessToken": token}).encode("utf-8")
    req = urllib.request.Request(
        PROFILE_URL,
        data=data,
        headers={"Content-Type": "application/json"},
        method="POST"
    )
    return do_request(req, "POST + JSON accessToken")


def do_request(req, desc):
    print(f"\n>>> 尝试: {desc}")
    try:
        with urllib.request.urlopen(req, timeout=10) as resp:
            body = resp.read().decode("utf-8")
            print(f"    Status: {resp.status}")
            print(f"    Body: {body[:500]}")
            return body
    except urllib.error.HTTPError as e:
        body = e.read().decode("utf-8")
        print(f"    Status: {e.code}")
        print(f"    Body: {body[:500]}")
        return None
    except Exception as e:
        print(f"    Error: {e}")
        return None


def main():
    token = os.environ.get("ACCESS_TOKEN", "").strip()
    if not token:
        token = input("请输入 ACCESS_TOKEN (或从 get_sso_token.py 输出中复制 access_token): ").strip()
    if not token:
        print("请先设置环境变量 ACCESS_TOKEN")
        print("例如: export ACCESS_TOKEN='eyJhbGciOiJSUzI1NiIs...'")
        return

    print(f"Token 长度: {len(token)}")
    print(f"Token 前缀: {token[:20]}...")

    results = {
        "GET Header": try_get_header(token),
        "GET Query": try_get_query(token),
        "POST Form": try_post_form(token),
        "POST JSON": try_post_json(token),
    }

    print("\n" + "=" * 60)
    print("汇总结果:")
    for name, body in results.items():
        status = "成功" if body else "失败"
        print(f"  {name}: {status}")


if __name__ == "__main__":
    main()
