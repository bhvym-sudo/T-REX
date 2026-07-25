import argparse
import asyncio
import json
import os
import random
import sys
from collections import deque
from urllib.parse import parse_qs, quote, urlencode, urlparse, urlunparse

from playwright.async_api import async_playwright


def emit(event_type, **values):
    message = {"type": event_type, **values}
    sys.stdout.write(json.dumps(message, separators=(",", ":"), ensure_ascii=True) + "\n")
    sys.stdout.flush()


async def rate_delay(min_seconds=2.0, max_seconds=4.0):
    await asyncio.sleep(random.uniform(min_seconds, max_seconds))


def tweet_ids(payload):
    found = set()

    def walk(value):
        if isinstance(value, dict):
            tweet_results = value.get("tweet_results")
            if isinstance(tweet_results, dict):
                result = tweet_results.get("result")
                while isinstance(result, dict) and isinstance(result.get("tweet"), dict):
                    result = result["tweet"]
                if isinstance(result, dict):
                    tweet_id = result.get("rest_id")
                    legacy = result.get("legacy")
                    if not tweet_id and isinstance(legacy, dict):
                        tweet_id = legacy.get("id_str")
                    if tweet_id:
                        found.add(str(tweet_id))
            for child in value.values():
                walk(child)
        elif isinstance(value, list):
            for child in value:
                walk(child)

    walk(payload)
    return found


def timeline_cursors(payload):
    cursors = []
    seen = set()

    def walk(value):
        if isinstance(value, dict):
            typename = value.get("__typename")
            entry_type = value.get("entryType")
            if typename == "TimelineTimelineCursor" or entry_type == "TimelineTimelineCursor":
                content = value.get("content")
                if not isinstance(content, dict):
                    content = {}
                cursor_type = str(value.get("cursorType") or content.get("cursorType") or "Unknown")
                cursor = value.get("value") or content.get("value")
                if cursor and cursor_type.lower() != "top" and cursor not in seen:
                    seen.add(cursor)
                    cursors.append({"value": cursor, "type": cursor_type})
            for child in value.values():
                walk(child)
        elif isinstance(value, list):
            for child in value:
                walk(child)

    walk(payload)
    return cursors


def replace_cursor(request_url, cursor):
    parsed = urlparse(request_url)
    parameters = parse_qs(parsed.query, keep_blank_values=True)
    try:
        variables = json.loads(parameters.get("variables", ["{}"])[0])
    except Exception:
        variables = {}
    variables["cursor"] = cursor
    variables["count"] = 20
    parameters["variables"] = [json.dumps(variables, separators=(",", ":"))]
    query = urlencode(parameters, doseq=True)
    return urlunparse(
        (parsed.scheme, parsed.netloc, parsed.path, parsed.params, query, parsed.fragment)
    )


def replay_headers(headers):
    headers = headers or {}
    allowed = (
        "accept",
        "accept-language",
        "authorization",
        "content-type",
        "x-client-transaction-id",
        "x-csrf-token",
        "x-twitter-active-user",
        "x-twitter-auth-type",
        "x-twitter-client-language",
    )
    result = {}
    for key in allowed:
        value = headers.get(key)
        if value:
            result[key] = value
    result.setdefault("accept", "*/*")
    result.setdefault("content-type", "application/json")
    return result


def search_url(query, result_mode):
    url = f"https://x.com/search?q={quote(query)}&src=typed_query"
    if result_mode == "latest":
        url += "&f=live"
    return url


async def browser_fetch(page, request_url, headers):
    return await page.evaluate(
        """
        async ({ url, headers }) => {
            const response = await fetch(url, {
                method: "GET",
                credentials: "include",
                headers
            });
            if (!response.ok) {
                throw new Error(`GraphQL ${response.status}`);
            }
            return await response.json();
        }
        """,
        {"url": request_url, "headers": headers},
    )


async def collect(request, profile_dir):
    query = str(request.get("query") or "").strip()
    result_mode = str(request.get("resultMode") or "latest").strip().lower()
    max_posts = int(request.get("maxPosts") or 5000)
    max_scrolls = int(request.get("maxScrolls") or 600)
    if not query:
        raise RuntimeError("search query is empty")
    if result_mode not in {"latest", "top"}:
        raise RuntimeError("result mode must be latest or top")
    if not os.path.isdir(profile_dir):
        raise RuntimeError(f"X Edge profile does not exist: {profile_dir}")

    target_url = search_url(query, result_mode)
    state = {
        "seen_posts": set(),
        "seen_payloads": set(),
        "seen_cursors": set(),
        "cursor_queue": deque(),
        "cursor_types": {},
        "template_url": "",
        "headers": {},
        "response_count": 0,
        "fetched_pages": 0,
        "failed_pages": 0,
        "cursor_replay_enabled": True,
        "last_failure_status": 0,
        "last_activity": 0,
        "rate_limited": False,
    }
    pending = set()
    task_errors = []
    ingest_lock = asyncio.Lock()
    scroll_state = {"round": 0}

    async with async_playwright() as playwright:
        emit("status", message="Opening authenticated headless Microsoft Edge...", progress=5)
        context = await playwright.chromium.launch_persistent_context(
            user_data_dir=profile_dir,
            channel="msedge",
            headless=True,
            viewport={"width": 1280, "height": 800},
            locale="en-US",
            args=[
                "--headless=new",
                "--disable-gpu",
                "--disable-extensions",
                "--disable-notifications",
                "--disable-component-update",
                "--no-first-run",
                "--no-default-browser-check",
            ],
        )
        page = context.pages[0] if context.pages else await context.new_page()

        cookies = await context.cookies(["https://x.com/", "https://twitter.com/"])
        if not any(cookie.get("name") == "auth_token" for cookie in cookies):
            await context.close()
            raise RuntimeError("the local Edge profile does not contain an authenticated X session")

        async def ingest(payload, source):
            async with ingest_lock:
                ids = tweet_ids(payload)
                cursors = timeline_cursors(payload)
                fingerprint = "|".join(sorted(ids))
                if cursors:
                    fingerprint += "|" + cursors[-1]["value"]
                if fingerprint and fingerprint in state["seen_payloads"]:
                    return 0, 0
                if fingerprint:
                    state["seen_payloads"].add(fingerprint)

                before = len(state["seen_posts"])
                for tweet_id in ids:
                    if len(state["seen_posts"]) >= max_posts:
                        break
                    state["seen_posts"].add(tweet_id)

                added_cursors = 0
                for cursor in cursors:
                    value = cursor["value"]
                    cursor_type = cursor["type"]
                    if value in state["seen_cursors"]:
                        continue
                    state["seen_cursors"].add(value)
                    if state["cursor_replay_enabled"]:
                        state["cursor_queue"].append(cursor)
                    state["cursor_types"][cursor_type] = (
                        state["cursor_types"].get(cursor_type, 0) + 1
                    )
                    added_cursors += 1

                added_posts = len(state["seen_posts"]) - before
                state["response_count"] += 1
                state["last_activity"] = scroll_state["round"]
                emit("payload", payload=payload)
                emit(
                    "status",
                    message=(
                        f"Captured {state['response_count']} SearchTimeline response(s) · "
                        f"{len(state['seen_posts'])} unique post(s) · {source}"
                    ),
                    progress=min(94, 10 + state["response_count"]),
                    responseCount=state["response_count"],
                    total=len(state["seen_posts"]),
                    added=added_posts,
                )
                return added_posts, added_cursors

        async def process_response(response):
            if "SearchTimeline" not in response.url or "/graphql/" not in response.url:
                return
            if response.status != 200:
                state["last_failure_status"] = response.status
                if response.status == 429:
                    state["rate_limited"] = True
                emit(
                    "status",
                    message=f"SearchTimeline returned HTTP {response.status}.",
                    progress=10,
                )
                return
            state["template_url"] = response.url
            try:
                request_headers = await response.request.all_headers()
            except Exception:
                request_headers = response.request.headers
            state["headers"] = replay_headers(request_headers)
            try:
                payload = await response.json()
            except Exception:
                return
            await ingest(payload, "browser response")

        def schedule_response(response):
            task = asyncio.create_task(process_response(response))
            pending.add(task)

            def finish(done):
                pending.discard(done)
                if done.cancelled():
                    return
                error = done.exception()
                if error is not None:
                    task_errors.append(str(error))

            task.add_done_callback(finish)

        async def wait_pending():
            if pending:
                await asyncio.gather(*list(pending), return_exceptions=True)
            if task_errors:
                raise RuntimeError(f"SearchTimeline response processing failed: {task_errors[0]}")

        async def drain_cursors(limit=300, empty_limit=18):
            fetched = 0
            empty_pages = 0
            total_added = 0
            while (
                state["cursor_queue"]
                and fetched < limit
                and len(state["seen_posts"]) < max_posts
                and not state["rate_limited"]
                and state["cursor_replay_enabled"]
            ):
                cursor = state["cursor_queue"].popleft()
                emit(
                    "status",
                    message=(
                        f"Fetching {cursor['type']} cursor page {state['fetched_pages'] + 1} · "
                        f"{len(state['seen_posts'])} unique post(s)"
                    ),
                    progress=min(94, 12 + state["fetched_pages"] // 3),
                )
                try:
                    payload = await browser_fetch(
                        page,
                        replace_cursor(state["template_url"], cursor["value"]),
                        state["headers"],
                    )
                except Exception as exc:
                    state["failed_pages"] += 1
                    if "429" in str(exc):
                        state["rate_limited"] = True
                        break
                    if "404" in str(exc):
                        state["cursor_replay_enabled"] = False
                        state["cursor_queue"].clear()
                        emit(
                            "status",
                            message=(
                                "Direct cursor replay is unavailable for this X session; "
                                "continuing with intercepted browser-scroll pagination."
                            ),
                            progress=14,
                        )
                        break
                    if state["failed_pages"] >= 6:
                        emit(
                            "status",
                            message=f"Cursor collection paused after repeated API errors: {exc}",
                            progress=94,
                        )
                        break
                    fetched += 1
                    await rate_delay(1.5, 3.0)
                    continue

                added_posts, _ = await ingest(payload, f"{cursor['type']} cursor")
                state["fetched_pages"] += 1
                state["failed_pages"] = 0
                fetched += 1
                total_added += added_posts
                if added_posts == 0:
                    empty_pages += 1
                else:
                    empty_pages = 0
                if empty_pages >= empty_limit:
                    break
                await rate_delay(1.2, 2.4)
            return total_added

        page.on("response", schedule_response)

        try:
            emit(
                "status",
                message=f"Opening X {result_mode.title()} search...",
                progress=8,
                url=target_url,
            )
            await page.goto(target_url, wait_until="commit", timeout=60000)
            await asyncio.sleep(3)
            await wait_pending()
            await drain_cursors(limit=300, empty_limit=18)

            no_activity_rounds = 0
            stable_height_rounds = 0
            previous_height = 0
            previous_total = len(state["seen_posts"])
            previous_responses = state["response_count"]
            stop_reason = "maximum scroll safety limit reached"

            for round_number in range(1, max_scrolls + 1):
                scroll_state["round"] = round_number
                if len(state["seen_posts"]) >= max_posts:
                    stop_reason = f"configured post limit reached ({max_posts})"
                    break
                if state["rate_limited"]:
                    stop_reason = (
                        f"X rate limit reached after {len(state['seen_posts'])} unique post(s)"
                    )
                    break

                try:
                    height = await page.evaluate(
                        "() => Math.max(document.body.scrollHeight, document.documentElement.scrollHeight)"
                    )
                    height = int(height or 0)
                except Exception:
                    height = 0

                for _ in range(4):
                    await page.mouse.wheel(0, random.randint(850, 1450))
                    await asyncio.sleep(random.uniform(0.25, 0.65))
                if round_number % 8 == 0:
                    try:
                        await page.keyboard.press("End")
                    except Exception:
                        pass
                await rate_delay(2.0, 4.0)
                try:
                    await page.wait_for_load_state("networkidle", timeout=2800)
                except Exception:
                    pass
                await rate_delay(0.8, 1.6)
                await wait_pending()
                await drain_cursors(limit=260, empty_limit=18)
                await wait_pending()

                activity = (
                    len(state["seen_posts"]) > previous_total
                    or state["response_count"] > previous_responses
                    or bool(state["cursor_queue"])
                )
                if activity:
                    no_activity_rounds = 0
                    previous_total = len(state["seen_posts"])
                    previous_responses = state["response_count"]
                else:
                    no_activity_rounds += 1

                if height > 0 and height == previous_height:
                    stable_height_rounds += 1
                else:
                    stable_height_rounds = 0
                    previous_height = height

                if round_number % 5 == 0:
                    emit(
                        "status",
                        message=(
                            f"Scrolling X {result_mode.title()} results · round {round_number} · "
                            f"{len(state['seen_posts'])} unique post(s) · "
                            f"{state['fetched_pages']} cursor page(s)"
                        ),
                        progress=min(94, 15 + round_number // 6),
                        responseCount=state["response_count"],
                        total=len(state["seen_posts"]),
                    )

                if (
                    round_number >= 12
                    and no_activity_rounds >= 8
                    and stable_height_rounds >= 6
                    and not state["cursor_queue"]
                    and not pending
                ):
                    stop_reason = "X reached a stable end of available search results"
                    break

            await wait_pending()
            await drain_cursors(limit=1200, empty_limit=22)
            await wait_pending()

            if state["response_count"] == 0:
                if state["last_failure_status"] == 429:
                    raise RuntimeError(
                        "X SearchTimeline is rate limited. Wait for the X rate-limit window to reset, then scan again."
                    )
                if state["last_failure_status"]:
                    raise RuntimeError(
                        f"X loaded no successful SearchTimeline responses; "
                        f"last HTTP status was {state['last_failure_status']}."
                    )
                raise RuntimeError(
                    "X loaded no SearchTimeline responses. Refresh the X session and confirm the search opens normally."
                )

            cursor_summary = ", ".join(
                f"{key}:{value}" for key, value in sorted(state["cursor_types"].items())
            )
            emit(
                "done",
                total=len(state["seen_posts"]),
                responseCount=state["response_count"],
                cursorPages=state["fetched_pages"],
                cursorTypes=cursor_summary or "none",
                reason=stop_reason,
                progress=100,
            )
        finally:
            await page.close()
            await context.close()


async def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--profile", required=True)
    args = parser.parse_args()
    line = sys.stdin.readline()
    if not line:
        raise RuntimeError("Go backend supplied no search request")
    request = json.loads(line)
    await collect(request, os.path.abspath(args.profile))


if __name__ == "__main__":
    try:
        asyncio.run(main())
    except KeyboardInterrupt:
        emit("error", message="search cancelled")
        raise SystemExit(130)
    except Exception as exc:
        emit("error", message=str(exc))
        raise SystemExit(1)
