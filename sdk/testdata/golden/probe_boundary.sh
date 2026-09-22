#!/usr/bin/env bash
# Adversarial boundary probes against the live System One endpoint.
#
# These are NOT golden fixtures: they probe where sdk/doc.go's claims are
# falsifiable. Results are recorded in PROBES.md. Nothing here is written to
# disk except stdout, and the API key is read only from $TYPESAFE_API_KEY.
#
# Usage:
#   set -a && . ./.secrets/test.env && set +a
#   ./sdk/testdata/golden/probe_boundary.sh

set -euo pipefail

if [ -z "${TYPESAFE_API_KEY:-}" ]; then
  echo "TYPESAFE_API_KEY is not set" >&2
  exit 1
fi

export TYPESAFE_BASE_URL="${TYPESAFE_BASE_URL:-https://openrouter.ai/api/v1}"
export TYPESAFE_DEFAULT_MODEL="${TYPESAFE_DEFAULT_MODEL:-typesafe/jev-1.13}"

python3 - <<'PY'
import json, os, ssl, time, urllib.request, urllib.error

KEY = os.environ["TYPESAFE_API_KEY"]
URL = os.environ["TYPESAFE_BASE_URL"].rstrip("/") + "/systemone"
DM = os.environ["TYPESAFE_DEFAULT_MODEL"]
CTX = ssl.create_default_context()

def post(raw):
    req = urllib.request.Request(
        URL, data=raw, method="POST",
        headers={"Content-Type": "application/json", "Authorization": "Bearer " + KEY})
    for a in range(4):
        try:
            with urllib.request.urlopen(req, timeout=60, context=CTX) as r:
                return r.status, r.read()
        except urllib.error.HTTPError as e:
            return e.code, e.read()
        except Exception:
            if a == 3:
                raise
            time.sleep(1.5 * (a + 1))

def probe(label, payload, with_model=True, raw=None):
    if raw is None:
        body = dict(payload)
        if with_model:
            body["model"] = DM
        raw = json.dumps(body, ensure_ascii=False).encode()
    st, out = post(raw)
    text = out.decode("utf-8", "replace")
    try:
        d = json.loads(text)
    except ValueError:
        d = text
    if isinstance(d, dict) and "error" in d:
        env = "error-envelope top_keys=%s" % sorted(d.keys())
        detail = d["error"].get("message", "")
        if isinstance(detail, str):
            try:
                json.loads(detail)
                kind = "message=JSON-array"
            except ValueError:
                kind = "message=PLAIN-STRING"
        else:
            kind = "message=%s" % type(detail).__name__
        extra = " code=%r" % (d["error"].get("code"),)
        print("%-40s HTTP %-4d %s %s%s\n%-40s   msg=%s" % (label, st, env, kind, extra, "", repr(detail)[:150]))
    elif isinstance(d, dict):
        print("%-40s HTTP %-4d 200 answers=%s" % (label, st, sorted(d.get("answers", {}))))
    else:
        print("%-40s HTTP %-4d %s" % (label, st, repr(d)[:150]))

Q = lambda t="noul", **kw: dict({"type": t, "instructions": "Is this a test?"}, **kw)
NOK = {"true": "yes", "false": "no"}

print("== doc.go:48/49/50 -- model, state, questions are required ==")
probe("no model field", {"state": "x", "questions": {"q": Q(criteria=NOK)}}, with_model=False)
probe("state missing", {"questions": {"q": Q(criteria=NOK)}})
probe("state = null", {"state": None, "questions": {"q": Q(criteria=NOK)}})
probe("state = number 42", {"state": 42, "questions": {"q": Q(criteria=NOK)}})
probe("state = nested object", {"state": {"a": [1, {"b": None}]}, "questions": {"q": Q(criteria=NOK)}})
probe("questions missing entirely", {"state": "x"})
probe("questions = {}", {"state": "x", "questions": {}})

print()
print("== doc.go:57 -- score criteria '>= 2 entries' ==")
probe("score, 0 criteria entries", {"state": "x", "questions": {"q": Q("score", criteria=[])}})
probe("score, 1 criteria entry", {"state": "x", "questions": {"q": Q("score", criteria=["0: only one"])}})
probe("score, 2 criteria entries", {"state": "x", "questions": {"q": Q("score", criteria=["0: low", "1: high"])}})
probe("score, criteria missing", {"state": "x", "questions": {"q": Q("score")}})

print()
print("== doc.go:55 -- noul criteria shape ==")
probe("noul, criteria missing", {"state": "x", "questions": {"q": Q()}})
probe("noul, criteria = null", {"state": "x", "questions": {"q": dict(Q(), criteria=None)}})
probe("noul, criteria = object", {"state": "x", "questions": {"q": Q(criteria=NOK)}})

print()
print("== doc.go:56 -- choice criteria shape ==")
probe("choice, criteria = {}", {"state": "x", "questions": {"q": Q("choice", criteria={})}})
probe("choice, criteria missing", {"state": "x", "questions": {"q": Q("choice")}})
probe("choice, criteria = array", {"state": "x", "questions": {"q": Q("choice", criteria=["a", "b"])}})

print()
print("== doc.go:53 -- strictness of the request decoder ==")
probe("unknown top-level field", {"state": "x", "questions": {"q": Q(criteria=NOK)}, "bogus": 1})
probe("unknown field in a question", {"state": "x", "questions": {"q": dict(Q(criteria=NOK), wat=1)}})

print()
print("== non-JSON request body ==")
probe("truncated JSON body", None, raw=b'{"model":"typesafe/jev-1.13","state":"x",')
PY
