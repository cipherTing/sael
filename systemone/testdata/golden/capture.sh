#!/usr/bin/env bash
# Re-capture the golden fixtures in this directory from the live OpenRouter
# TypeSafe "System One" endpoint.
#
# The API key is read ONLY from the environment (TYPESAFE_API_KEY). It is
# never written to disk, never echoed, and never embedded in a fixture: the
# fixtures record only the request body, the response body and the HTTP status.
#
# Target selection is vendor neutral, matching the SDK:
#   TYPESAFE_BASE_URL       default https://openrouter.ai/api/v1
#   TYPESAFE_DEFAULT_MODEL  default typesafe/jev-1.13
#   TYPESAFE_API_KEY        required, never logged
#
# Usage:
#   set -a && . ./.secrets/test.env && set +a
#   ./systemone/testdata/golden/capture.sh
#
# Requires: python3 (stdlib only), network access.

set -euo pipefail

if [ -z "${TYPESAFE_API_KEY:-}" ]; then
  echo "TYPESAFE_API_KEY is not set" >&2
  exit 1
fi

OUT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
export OUT_DIR
export TYPESAFE_BASE_URL="${TYPESAFE_BASE_URL:-https://openrouter.ai/api/v1}"
export TYPESAFE_DEFAULT_MODEL="${TYPESAFE_DEFAULT_MODEL:-typesafe/jev-1.13}"

python3 - <<'PY'
import json, os, ssl, sys, time, urllib.error, urllib.request

OUT = os.environ["OUT_DIR"]
KEY = os.environ["TYPESAFE_API_KEY"]
URL = os.environ["TYPESAFE_BASE_URL"].rstrip("/") + "/systemone"
DEFAULT_MODEL = os.environ["TYPESAFE_DEFAULT_MODEL"]
CTX = ssl.create_default_context()

def call(name, body, expect, note, auth=True, model=None, retries=3):
    payload = dict(body)
    payload["model"] = model or DEFAULT_MODEL
    data = json.dumps(payload, ensure_ascii=False).encode("utf-8")
    headers = {"Content-Type": "application/json"}
    if auth:
        headers["Authorization"] = "Bearer " + KEY
    last = None
    for attempt in range(retries):
        req = urllib.request.Request(URL, data=data, headers=headers, method="POST")
        try:
            with urllib.request.urlopen(req, timeout=60, context=CTX) as r:
                status, raw = r.status, r.read()
        except urllib.error.HTTPError as e:
            status, raw = e.code, e.read()
        except Exception as e:  # network hiccup -> retry
            last = "%s: %s" % (type(e).__name__, e)
            time.sleep(1.5 * (attempt + 1))
            continue
        text = raw.decode("utf-8", "replace")
        try:
            resp = json.loads(text)
        except ValueError:
            resp = text
        # Only retry on a transient server error; 4xx is a real answer.
        if status >= 500 and attempt < retries - 1:
            last = "HTTP %d" % status
            time.sleep(1.5 * (attempt + 1))
            continue
        fixture = {
            "note": note,
            "request": payload,
            "request_authorized": auth,
            "status": status,
            "response": resp,
        }
        path = os.path.join(OUT, name)
        with open(path, "w", encoding="utf-8") as f:
            json.dump(fixture, f, ensure_ascii=False, indent=2)
            f.write("\n")
        got = "OK" if status == expect else "UNEXPECTED (expected %d)" % expect
        print("%-38s HTTP %d  %s" % (name, status, got))
        return status
    print("%-38s FAILED after %d attempts (%s)" % (name, retries, last))
    return None

results = {}

# 1a. noul, single question, string instructions, no criteria.
results["noul_string.json"] = call(
    "noul_string.json",
    {"state": "The customer wrote: 'My invoice was charged twice this month.'",
     "questions": {"was_charged_twice": {
         "type": "noul",
         "instructions": "Is the customer reporting a duplicate charge?"}}},
    200, "noul primitive, string instructions, no criteria key")

# 1b. noul, explicit true/false criteria.
results["noul_criteria.json"] = call(
    "noul_criteria.json",
    {"state": "Our SLA promises a 4 hour first response. The ticket was answered in 19 hours.",
     "questions": {"sla_breached": {
         "type": "noul",
         "instructions": "Did we breach the stated SLA?",
         "criteria": {
             "true": "The actual first response time exceeds the promised 4 hours.",
             "false": "The first response arrived within the promised 4 hours."}}}},
    200, "noul primitive with explicit true/false criteria entries")

# 2. choice, one option has a null description.
results["choice_null_description.json"] = call(
    "choice_null_description.json",
    {"state": "The mobile app crashes on launch after the latest update. Restarting does not help.",
     "questions": {"category": {
         "type": "choice",
         "instructions": "Which support queue should this ticket go to?",
         "criteria": {
             "billing": "Payment, invoices, refunds, subscriptions.",
             "tech": "Crashes, bugs, performance, device problems.",
             "other": None}}}},
    200, "choice primitive; the 'other' option carries a null description")

# 3. score, 4 level rubric.
results["score_rubric.json"] = call(
    "score_rubric.json",
    {"state": "The package arrived three days late and the outer box was crushed, but the item inside was undamaged.",
     "questions": {"severity": {
         "type": "score",
         "instructions": "Rate the severity of this delivery problem.",
         "criteria": [
             "0: No problem at all.",
             "1: Minor cosmetic or timing issue, no real impact.",
             "2: Real inconvenience or damage that needs compensation.",
             "3: Severe: unusable product or a safety concern."]}}},
    200, "score primitive with a 4 level rubric")

# 4. all three primitives in one call, state is a JSON object.
results["mixed_primitives_object_state.json"] = call(
    "mixed_primitives_object_state.json",
    {"state": {
        "ticket_id": "T-1042",
        "channel": "email",
        "subject": "Charged twice and the app will not open",
        "body": "You billed me twice for September, and since the update the app crashes before the login screen. I want the extra charge refunded.",
        "customer_tenure_months": 14,
        "plan": "pro"},
     "questions": {
         "is_urgent": {"type": "noul",
             "instructions": "Does this ticket need a same day human response?",
             "criteria": {"true": "Money is at stake or the product is unusable.",
                          "false": "It is a question or a cosmetic issue."}},
         "primary_intent": {"type": "choice",
             "instructions": "What is the customer mainly asking for?",
             "criteria": {"refund": "They want money returned.",
                          "bug_fix": "They want the crash fixed.",
                          "info": "They only want information.",
                          "cancel": None}},
         "sentiment": {"type": "score",
             "instructions": "How negative is the customer's tone?",
             "criteria": ["0: Neutral or positive.",
                          "1: Mildly annoyed.",
                          "2: Clearly frustrated.",
                          "3: Angry or threatening to leave."]}}},
    200, "noul + choice + score in one call, state is a JSON object")

# 5. state is a plain string.
results["state_string.json"] = call(
    "state_string.json",
    {"state": "The build passed all 412 tests and the deploy took 90 seconds.",
     "questions": {"all_tests_passed": {"type": "noul",
         "instructions": "Did every test pass?",
         "criteria": {"true": "The text says all tests passed.",
                      "false": "Some test failed or the result is unclear."}}}},
    200, "state is a bare JSON string")

# 6. state is an array of strings.
results["state_array.json"] = call(
    "state_array.json",
    {"state": ["login page: blank screen on Safari",
               "checkout: coupon field rejects valid codes",
               "search: results are one release behind"],
     "questions": {"bug_count": {"type": "choice",
         "instructions": "How many of these entries describe a defect?",
         "criteria": {"none": "None of them.", "one": "Exactly one.",
                      "two": "Exactly two.", "all_three": None}}}},
    200, "state is a JSON array of strings")

# 7. error: model that does not exist.
results["error_model_not_found.json"] = call(
    "error_model_not_found.json",
    {"state": "anything",
     "questions": {"q": {"type": "noul", "instructions": "Is this a test?",
                         "criteria": {"true": "yes", "false": "no"}}}},
    400, "error case: non-existent model id must be rejected",
    model="typesafe/does-not-exist-9999")

# 8. error: unknown question type.
results["error_bad_question_type.json"] = call(
    "error_bad_question_type.json",
    {"state": "anything",
     "questions": {"q": {"type": "bogus", "instructions": "Is this a test?"}}},
    400, "error case: unknown question type 'bogus'")

# 9. error: empty questions.
results["error_empty_questions.json"] = call(
    "error_empty_questions.json",
    {"state": "anything", "questions": {}},
    400, "error case: empty questions object")

# 10. error: no Authorization header at all.
results["error_no_auth.json"] = call(
    "error_no_auth.json",
    {"state": "anything",
     "questions": {"q": {"type": "noul", "instructions": "Is this a test?",
                         "criteria": {"true": "yes", "false": "no"}}}},
    401, "error case: request sent WITHOUT an Authorization header", auth=False)

# 11. long Chinese state, English questions.
zh = (
    "上周我在贵站购买了一台便携显示器，订单号 20260915003。商品页面写明支持 65W 反向供电，"
    "但我收到后实测只能给手机充电，接上笔记本时显示器会反复黑屏重启。我先后联系了在线客服三次："
    "第一次客服让我换一根线，换了原装线之后问题依旧；第二次客服说需要升级固件，发来的固件包解压后是损坏的，"
    "无法安装；第三次客服直接建议我退货，但退货页面要求我承担往返运费，而我认为这是商品描述与实物不符，"
    "运费不应该由我承担。现在距离我下单已经过去十一天，七天无理由退货的窗口已经关闭，"
    "客服又说要等仓库检测结果，需要七到十五个工作日。我希望能尽快得到一个明确的处理方案："
    "要么换一台真正支持 65W 供电的机器，要么全额退款并由你们承担退回运费。"
)
results["chinese_state.json"] = call(
    "chinese_state.json",
    {"state": zh,
     "questions": {
         "is_product_misdescribed": {"type": "noul",
             "instructions": "Does the customer claim the product does not match its listing?",
             "criteria": {"true": "The customer says the delivered item cannot do what the listing promised.",
                          "false": "The customer complains about something else."}},
         "primary_request": {"type": "choice",
             "instructions": "What outcome is the customer asking for?",
             "criteria": {"replacement": "A working replacement unit.",
                          "full_refund": "A full refund, with the seller paying return shipping.",
                          "repair": "A repair or firmware fix.",
                          "explanation": None}},
         "churn_risk": {"type": "score",
             "instructions": "How likely is this customer to stop buying from us?",
             "criteria": ["0: No sign of leaving.",
                          "1: Mild frustration only.",
                          "2: Explicitly considering alternatives.",
                          "3: Has decided to leave or is demanding escalation."]}}},
    200, "long Chinese state with English questions (Chinese language behaviour)")

print()
print("summary:", json.dumps(results, ensure_ascii=False))
PY
