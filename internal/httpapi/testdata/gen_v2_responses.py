"""Records how the 2.0 (Python) API answers the requests of phase 2's routes.

Run from the repository root with the Python 2.0 dependencies installed
(requirements.txt):

    python internal/httpapi/testdata/gen_v2_responses.py

It writes v2_responses.json next to this script. The Go test
TestConformanceWith2_0 replays every request, in order, against the Go
server configured the same way and compares status, headers and body.

Each scenario starts a fresh 2.0 application on an empty database (or on a
copy of a fixture database) with a fixed SECRET_KEY. Requests are raw bytes
so that Go sends exactly the same thing. "{admin}" and "{alice}" in a header
stand for the access token returned by the last successful login of that
user; the other "{name}" placeholders are the fixed tokens in "tokens".

Values that differ on every run are normalized on both sides (normalize()):
access tokens become "<jwt>", timestamps "<datetime:Z>" or "<datetime:naive>".
"""

import json
import re
import shutil
import sys
import tempfile
from pathlib import Path

HERE = Path(__file__).resolve().parent
ROOT = HERE.parents[2]
sys.path.insert(0, str(ROOT))

import bcrypt  # noqa: E402
import jwt  # noqa: E402
from fastapi.testclient import TestClient  # noqa: E402

from app.config import Settings  # noqa: E402
from app.main import create_app  # noqa: E402

SECRET_KEY = "conformance-secret-key-conformance-secret-key"
ADMIN_PASSWORD = "admin-password"
ORIGIN = "https://dashboard.example.com"

# exp 4102444800 is 2100-01-01; 1600000000 is 2020-09-13.
TOKENS = {
    "expired": jwt.encode({"sub": "admin", "iat": 1599990000, "exp": 1600000000}, SECRET_KEY),
    "forged": jwt.encode({"sub": "admin", "exp": 4102444800}, "another-key-another-key-another"),
    "ghost": jwt.encode({"sub": "ghost", "exp": 4102444800}, SECRET_KEY),
    "no_exp": jwt.encode({"sub": "admin"}, SECRET_KEY),
    "no_sub": jwt.encode({"exp": 4102444800}, SECRET_KEY),
    "empty_sub": jwt.encode({"sub": "", "exp": 4102444800}, SECRET_KEY),
    "int_sub": jwt.encode({"sub": 1, "exp": 4102444800}, SECRET_KEY),
    "hs512": jwt.encode({"sub": "admin", "exp": 4102444800}, SECRET_KEY * 2, algorithm="HS512"),
    "alg_none": jwt.encode({"sub": "admin", "exp": 4102444800}, None, algorithm="none"),
    "future_iat": jwt.encode({"sub": "admin", "iat": 4102444000, "exp": 4102444800}, SECRET_KEY),
    "future_nbf": jwt.encode({"sub": "admin", "nbf": 4102444000, "exp": 4102444800}, SECRET_KEY),
    "with_aud": jwt.encode({"sub": "admin", "aud": "x", "exp": 4102444800}, SECRET_KEY),
    "admin_long": jwt.encode({"sub": "admin", "iat": 1700000000, "exp": 4102444800}, SECRET_KEY),
}

FORM = {"Content-Type": "application/x-www-form-urlencoded"}
JSON = {"Content-Type": "application/json"}
BOUNDARY = "conformance-boundary"


def bearer(name: str) -> dict:
    return {"Authorization": f"Bearer {{{name}}}"}


def multipart(fields: dict[str, str]) -> str:
    parts = [
        f'--{BOUNDARY}\r\nContent-Disposition: form-data; name="{k}"\r\n\r\n{v}\r\n'
        for k, v in fields.items()
    ]
    return "".join(parts) + f"--{BOUNDARY}--\r\n"


def req(name, method, path, headers=None, body="", save_token=None, html=False):
    return {
        "name": name,
        "method": method,
        "path": path,
        "headers": dict(headers or {}),
        "body": body,
        "save_token": save_token,
        "html": html,
    }


def form_login(name, username, password, save=None, extra=""):
    body = f"username={username}&password={password}{extra}"
    return req(name, "POST", "/login/access-token", FORM, body, save_token=save)


def users_post(name, body, token="admin", content_type=JSON):
    headers = {**content_type, **(bearer(token) if token else {})}
    return req(name, "POST", "/users", headers, body)


A = bearer("admin")
LONG = "x" * 100

MAIN = [
    req("health", "GET", "/health"),
    req("health head", "HEAD", "/health"),
    req("health post", "POST", "/health"),
    req("root redirects to docs", "GET", "/"),
    req("docs", "GET", "/docs", html=True),
    req("openapi", "GET", "/openapi.json", html=True),
    req("unknown path without token", "GET", "/nope"),
    req("unknown path with token", "GET", "/nope", A),
    # Login.
    form_login("login admin", "admin", ADMIN_PASSWORD, save="admin"),
    form_login("login wrong password", "admin", "wrong"),
    form_login("login unknown user", "nobody", ADMIN_PASSWORD),
    form_login("login long password", "admin", LONG),
    form_login("login empty password", "admin", ""),
    form_login("login empty username", "", ADMIN_PASSWORD),
    req("login missing password", "POST", "/login/access-token", FORM, "username=admin"),
    req("login missing both", "POST", "/login/access-token", FORM, ""),
    req("login no body", "POST", "/login/access-token"),
    req(
        "login json body",
        "POST",
        "/login/access-token",
        JSON,
        json.dumps({"username": "admin", "password": ADMIN_PASSWORD}),
    ),
    form_login("login grant_type password", "admin", ADMIN_PASSWORD, extra="&grant_type=password"),
    form_login("login grant_type other", "admin", ADMIN_PASSWORD, extra="&grant_type=client"),
    form_login("login with scope", "admin", ADMIN_PASSWORD, extra="&scope=a%20b&client_id=c"),
    form_login("login percent-encoded", "adm%69n", ADMIN_PASSWORD.replace("-", "%2D")),
    req(
        "login multipart",
        "POST",
        "/login/access-token",
        {"Content-Type": f"multipart/form-data; boundary={BOUNDARY}"},
        multipart({"username": "admin", "password": ADMIN_PASSWORD}),
    ),
    req("login get", "GET", "/login/access-token"),
    # Bearer tokens.
    req("me without token", "GET", "/users/me"),
    req("me garbage token", "GET", "/users/me", {"Authorization": "Bearer garbage"}),
    req("me basic scheme", "GET", "/users/me", {"Authorization": "Basic YWRtaW46eA=="}),
    req("me bearer without token", "GET", "/users/me", {"Authorization": "Bearer"}),
    req("me bearer and space", "GET", "/users/me", {"Authorization": "Bearer "}),
    req("me lower-case scheme", "GET", "/users/me", {"Authorization": "bearer {admin}"}),
    req("me extra spaces", "GET", "/users/me", {"Authorization": "Bearer   {admin}  "}),
    req("me tab separator", "GET", "/users/me", {"Authorization": "Bearer\t{admin}"}),
    *[req(f"me {name} token", "GET", "/users/me", bearer(name)) for name in TOKENS],
    req("me", "GET", "/users/me", A),
    # Creating users.
    users_post("create without token", '{"username":"alice","password":"alice-password"}', None),
    users_post("create alice", '{"username":"alice","password":"alice-password"}'),
    users_post("create alice again", '{"username":"alice","password":"other-password"}'),
    users_post("create short username", '{"username":"al","password":"long-enough"}'),
    users_post("create long username", '{"username":"a234567890123456789012","password":"long-enough"}'),
    users_post("create username with space", '{"username":"has space","password":"long-enough"}'),
    users_post("create unicode username", '{"username":"élise","password":"long-enough"}'),
    users_post("create short password", '{"username":"dave","password":"short"}'),
    users_post("create 73-byte password", json.dumps({"username": "dave", "password": "x" * 73})),
    users_post("create 37 two-byte chars", json.dumps({"username": "dave", "password": "é" * 37})),
    users_post("create both invalid", '{"username":"a b","password":"short"}'),
    users_post("create missing password", '{"username":"dave"}'),
    users_post("create missing both", "{}"),
    users_post("create int username", '{"username":123,"password":"long-enough"}'),
    users_post("create null password", '{"username":"dave","password":null}'),
    users_post("create bool password", '{"username":"dave","password":true}'),
    users_post("create array body", "[]"),
    users_post("create string body", '"text"'),
    users_post("create number body", "42"),
    users_post("create null body", "null"),
    users_post("create malformed json", '{"username": "dave",'),
    users_post("create empty body", ""),
    users_post("create form body", "username=dave&password=long-enough", content_type=FORM),
    users_post("create no content type", '{"username":"dave","password":"long-enough"}', content_type={}),
    users_post("create text content type", '{"username":"dave","password":"long-enough"}', content_type={"Content-Type": "text/plain"}),
    users_post("create json charset", '{"username":"dave","password":"long-enough"}', content_type={"Content-Type": "application/json; charset=utf-8"}),
    users_post("create extra field", '{"username":"erin","password":"long-enough","admin":true}'),
    users_post("create 72-byte password", json.dumps({"username": "frank", "password": "密" * 24})),
    users_post("create dotted username", '{"username":"g.h-i_j","password":"long-enough"}'),
    users_post("create invalid token", '{"username":"dave","password":"long-enough"}', "expired"),
    users_post("create malformed json without token", '{"username":', None),
    req("create trailing slash", "POST", "/users/", {**JSON, **A}, '{"username":"zed","password":"long-enough"}'),
    # Users as alice.
    form_login("login alice", "alice", "alice-password", save="alice"),
    req("alice me", "GET", "/users/me", bearer("alice")),
    req("alice lists users", "GET", "/users", bearer("alice")),
    users_post("alice creates a user", '{"username":"bob","password":"bob-password"}', "alice"),
    users_post("alice creates an invalid user", '{"username":"b"}', "alice"),
    req("alice gets admin", "GET", "/users/admin", bearer("alice")),
    req("alice deletes admin", "DELETE", "/users/admin", bearer("alice")),
    # Users as admin.
    req("list users", "GET", "/users", A),
    req("list users trailing slash", "GET", "/users/", A),
    req("get alice", "GET", "/users/alice", A),
    req("get unknown user", "GET", "/users/nobody", A),
    req("get encoded space", "GET", "/users/a%20b", A),
    req("get encoded slash", "GET", "/users/a%2Fb", A),
    req("get without token", "GET", "/users/alice"),
    req("put user", "PUT", "/users/alice", A),
    req("delete admin", "DELETE", "/users/admin", A),
    req("delete unknown user", "DELETE", "/users/nobody", A),
    req("delete without token", "DELETE", "/users/alice"),
    req("delete alice", "DELETE", "/users/alice", A),
    req("deleted user's token", "GET", "/users/me", bearer("alice")),
    req("deleted user logs in", "POST", "/login/access-token", FORM, "username=alice&password=alice-password"),
    req("delete alice again", "DELETE", "/users/alice", A),
    req("options without cors", "OPTIONS", "/users", {"Origin": ORIGIN, "Access-Control-Request-Method": "GET"}),
    req("list users at the end", "GET", "/users", A),
]

CORS = [
    req("allowed origin", "GET", "/health", {"Origin": ORIGIN}),
    req("other origin", "GET", "/health", {"Origin": "https://evil.example.com"}),
    req("no origin", "GET", "/health"),
    req(
        "preflight",
        "OPTIONS",
        "/users",
        {
            "Origin": ORIGIN,
            "Access-Control-Request-Method": "POST",
            "Access-Control-Request-Headers": "authorization,content-type",
        },
    ),
    req(
        "preflight unknown path",
        "OPTIONS",
        "/monitors",
        {"Origin": ORIGIN, "Access-Control-Request-Method": "GET"},
    ),
    req(
        "preflight other origin",
        "OPTIONS",
        "/users",
        {"Origin": "https://evil.example.com", "Access-Control-Request-Method": "POST"},
    ),
    req("preflight without request method", "OPTIONS", "/users", {"Origin": ORIGIN}),
    req("error with allowed origin", "GET", "/users/me", {"Origin": ORIGIN}),
    form_login("login with allowed origin", "admin", ADMIN_PASSWORD, save="admin"),
]
CORS[-1]["headers"]["Origin"] = ORIGIN

CORS_ANY = [
    req("any origin", "GET", "/health", {"Origin": ORIGIN}),
    req("any origin with cookie", "GET", "/health", {"Origin": ORIGIN, "Cookie": "a=b"}),
    req(
        "any origin preflight",
        "OPTIONS",
        "/users",
        {"Origin": ORIGIN, "Access-Control-Request-Method": "DELETE"},
    ),
]

UPGRADE_V1 = [
    form_login("v1 legacy login", "legacy", "legacy-password", save="legacy"),
    req("v1 legacy me", "GET", "/users/me", bearer("legacy")),
    form_login("v1 old admin password", "admin", "old-admin-password"),
    form_login("v1 admin login", "admin", ADMIN_PASSWORD, save="admin"),
    req("v1 list users", "GET", "/users", A),
]

RECORDED_HEADERS = [
    "content-type",
    "www-authenticate",
    "location",
    "allow",
    "vary",
    "access-control-allow-origin",
    "access-control-allow-credentials",
    "access-control-allow-methods",
    "access-control-allow-headers",
    "access-control-max-age",
    "access-control-expose-headers",
]

JWT_RE = re.compile(r"^[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+$")
DATETIME_RE = re.compile(r"^\d{4}-\d\d-\d\dT\d\d:\d\d:\d\d(\.\d+)?(Z|[+-]\d\d:\d\d)?$")


def normalize(value):
    """Same rules as normalize() in conformance_test.go."""
    if isinstance(value, dict):
        out = {}
        for k, v in value.items():
            if k == "access_token" and isinstance(v, str) and JWT_RE.match(v):
                out[k] = "<jwt>"
            elif k in ("created_at", "last_visit") and isinstance(v, str) and DATETIME_RE.match(v):
                out[k] = "<datetime:Z>" if v.endswith("Z") else "<datetime:naive>"
            else:
                out[k] = normalize(v)
        return out
    if isinstance(value, list):
        return [normalize(v) for v in value]
    return value


def run(settings: Settings, steps: list[dict]) -> list[dict]:
    saved: dict[str, str] = {}
    out = []
    with TestClient(create_app(settings)) as client:
        for step in steps:
            headers = {}
            for k, v in step["headers"].items():
                for name, token in {**TOKENS, **saved}.items():
                    v = v.replace(f"{{{name}}}", token)
                headers[k] = v
            resp = client.request(
                step["method"],
                step["path"],
                headers=headers,
                content=step["body"].encode(),
                follow_redirects=False,
            )
            ct = resp.headers.get("content-type", "")
            if step["html"] or step["method"] == "HEAD":
                body = None
            elif ct.startswith("application/json"):
                body = normalize(resp.json())
            else:
                body = resp.text
            if step["save_token"] and resp.status_code == 200:
                saved[step["save_token"]] = resp.json()["access_token"]
            out.append(
                {
                    **{k: step[k] for k in ("name", "method", "path", "headers", "body")},
                    "save_token": step["save_token"],
                    "html": step["html"],
                    "status": resp.status_code,
                    "response_headers": {
                        h: resp.headers[h] for h in RECORDED_HEADERS if h in resp.headers
                    },
                    "response_body": body,
                }
            )
    return out


def main() -> None:
    real_gensalt = bcrypt.gensalt
    bcrypt.gensalt = lambda rounds=4, prefix=b"2b": real_gensalt(4, prefix)

    scenarios = {}
    with tempfile.TemporaryDirectory() as tmp:

        def settings(name: str, **overrides) -> Settings:
            return Settings(
                _env_file=None,
                KUMA_SERVER="http://kuma.invalid:3001",
                KUMA_USERNAME="kuma-user",
                KUMA_PASSWORD="kuma-password",
                ADMIN_PASSWORD=ADMIN_PASSWORD,
                SECRET_KEY=SECRET_KEY,
                DB_PATH=Path(tmp) / name / "db.sqlite3",
                **overrides,
            )

        scenarios["main"] = {"cors_origins": [], "steps": run(settings("main"), MAIN)}
        scenarios["cors"] = {
            "cors_origins": [ORIGIN],
            "steps": run(settings("cors", BACKEND_CORS_ORIGINS=[ORIGIN]), CORS),
        }
        scenarios["cors_any"] = {
            "cors_origins": ["*"],
            "steps": run(settings("cors_any", BACKEND_CORS_ORIGINS=["*"]), CORS_ANY),
        }
        v1 = Path(tmp) / "v1"
        v1.mkdir()
        shutil.copy(ROOT / "internal/store/testdata/v1/db.sqlite3", v1 / "db.sqlite3")
        scenarios["upgrade_v1"] = {
            "cors_origins": [],
            "database": "internal/store/testdata/v1/db.sqlite3",
            "steps": run(settings("v1"), UPGRADE_V1),
        }

    doc = {
        "secret_key": SECRET_KEY,
        "admin_password": ADMIN_PASSWORD,
        "tokens": TOKENS,
        "scenarios": scenarios,
    }
    path = HERE / "v2_responses.json"
    path.write_text(json.dumps(doc, indent=1, ensure_ascii=False) + "\n", encoding="utf-8")


if __name__ == "__main__":
    main()
