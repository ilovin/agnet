#!/usr/bin/env python3
"""
Dashboard Screenshot Test - Capture dashboard UI for verification
Uses existing Chrome tab at localhost:7374
"""
import asyncio
import os
import sys
from playwright.async_api import async_playwright

AGENTGW_URL = "http://localhost:7374"
SCREENSHOT_DIR = "/tmp"


async def capture_dashboard():
    print("=== Dashboard Screenshot Test ===\n")
    print(f"Target URL: {AGENTGW_URL}/dashboard")
    print(f"Screenshot dir: {SCREENSHOT_DIR}\n")

    async with async_playwright() as p:
        browser = await p.chromium.launch(headless=True)
        page = await browser.new_page(viewport={"width": 1440, "height": 900})

        # Capture console logs and errors
        logs = []

        def handle_console(msg):
            text = f"[{msg.type}] {msg.text}"
            logs.append(text)

        def handle_page_error(err):
            text = f"[PAGE ERROR] {err}"
            logs.append(text)
            print(text)

        page.on("console", handle_console)
        page.on("pageerror", handle_page_error)

        try:
            # Step 1: Navigate to dashboard
            print("[1/4] Navigating to dashboard...")
            await page.goto(f"{AGENTGW_URL}/dashboard", timeout=60000)
            print("  Page loaded\n")

            # Step 2: Wait for Flutter to load
            print("[2/4] Waiting for Flutter Web to initialize...")
            await asyncio.sleep(15)

            # Check Flutter state
            flutter_state = await page.evaluate("""() => {
                return {
                    hasFlutter: !!window._flutter,
                    hasLoader: !!(window._flutter && window._flutter.loader),
                    documentReady: document.readyState,
                    title: document.title,
                    bodyText: document.body ? document.body.innerText.slice(0, 200) : 'no body'
                };
            }""")
            print(f"  Flutter state: {flutter_state}")
            print("  Waited 15s for Flutter load\n")

            # Step 3: Take full-page screenshot
            print("[3/4] Capturing full-page screenshot...")
            dashboard_path = os.path.join(SCREENSHOT_DIR, "dashboard_screenshot.png")
            await page.screenshot(path=dashboard_path, full_page=True)
            print(f"  Screenshot saved: {dashboard_path}")

            # Also take a viewport-sized screenshot
            viewport_path = os.path.join(SCREENSHOT_DIR, "dashboard_viewport.png")
            await page.screenshot(path=viewport_path, full_page=False)
            print(f"  Viewport screenshot saved: {viewport_path}\n")

            # Step 4: Check page content
            print("[4/4] Checking page content...")
            page_info = await page.evaluate("""() => {
                return {
                    url: window.location.href,
                    title: document.title,
                    bodyText: document.body ? document.body.innerText : 'no body'
                };
            }""")
            print(f"  URL: {page_info['url']}")
            print(f"  Title: {page_info['title']}")
            print(f"  Body text preview: {page_info['bodyText'][:300]}\n")

            # Console logs summary
            if logs:
                print("=== Console Logs (first 20) ===")
                for log in logs[:20]:
                    print(f"  {log}")
                print("")

            print("=== Test Complete ===")
            print(f"Dashboard screenshot: {dashboard_path}")
            print(f"Viewport screenshot:  {viewport_path}")

            await browser.close()
            return {
                "success": True,
                "dashboard_path": dashboard_path,
                "viewport_path": viewport_path,
                "flutter_state": flutter_state,
                "page_info": page_info,
            }

        except Exception as err:
            print(f"\n[ERROR] Test failed: {err}")

            # Save error screenshot
            error_path = os.path.join(SCREENSHOT_DIR, "dashboard_error.png")
            await page.screenshot(path=error_path, full_page=True)
            print(f"Error screenshot saved: {error_path}")

            await browser.close()
            return {"success": False, "error": str(err), "error_path": error_path}


if __name__ == "__main__":
    result = asyncio.run(capture_dashboard())
    if not result.get("success"):
        sys.exit(1)
