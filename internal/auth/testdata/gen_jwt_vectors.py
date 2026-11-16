"""JWT vectors for the Go tests, produced with 2.0's PyJWT and app.security.

Run from the root of a checkout of tag v2.0.0 (the Python sources are no
longer on this branch), with its requirements.txt installed and this script
copied there:

    python internal/auth/testdata/gen_jwt_vectors.py          # writes jwt_vectors.json
    python internal/auth/testdata/gen_jwt_vectors.py --check  # verifies go_tokens.json

jwt_vectors.json:
  issued   tokens PyJWT encodes for fixed claims; Go must produce the same bytes.
  decoded  tokens with 2.0's verdict (app.security.decode_access_token):
           the subject it returns, or null when it raises InvalidTokenError.

--check decodes every token of go_tokens.json (written by the Go test
TestGoTokensVerifyWithPyJWT with -update) with decode_access_token and fails
unless each yields the expected subject.
"""

import base64
import hashlib
import hmac
import json
import sys
from pathlib import Path

HERE = Path(__file__).resolve().parent
ROOT = HERE.parents[2]
sys.path.insert(0, str(ROOT))

import jwt  # noqa: E402

from app.security import decode_access_token  # noqa: E402

KEY = "jwt-vector-key-jwt-vector-key-jwt-vector-key"
OTHER_KEY = "another-key-another-key-another-key"
FAR = 4102444800  # 2100-01-01
PAST = 1600000000  # 2020-09-13


def b64(data: bytes) -> str:
    return base64.urlsafe_b64encode(data).rstrip(b"=").decode()


def raw_token(header: dict | str, payload: dict | str, key: str = KEY) -> str:
    """A token with exactly this header and payload JSON, signed with HS256."""
    h = header if isinstance(header, str) else json.dumps(header, separators=(",", ":"))
    p = payload if isinstance(payload, str) else json.dumps(payload, separators=(",", ":"))
    signing_input = f"{b64(h.encode())}.{b64(p.encode())}"
    sig = hmac.new(key.encode(), signing_input.encode(), hashlib.sha256).digest()
    return f"{signing_input}.{b64(sig)}"


def issued() -> list[dict]:
    out = []
    for sub in ["alice", "admin", "élise", 'quote"back\\slash', "tab\tnew\nline", "emoji 😀", "<&>"]:
        payload = {"sub": sub, "iat": 1700000000, "exp": 1700000000 + 691200}
        out.append({**payload, "token": jwt.encode(payload, KEY, algorithm="HS256")})
    return out


def candidates() -> list[tuple[str, str]]:
    hs256 = {"alg": "HS256", "typ": "JWT"}
    valid = {"sub": "alice", "iat": 1700000000, "exp": FAR}
    good = jwt.encode(valid, KEY)
    h, p, s = good.split(".")
    return [
        ("valid", good),
        ("valid without iat", jwt.encode({"sub": "alice", "exp": FAR}, KEY)),
        ("valid without typ", raw_token({"alg": "HS256"}, valid)),
        ("valid with kid", jwt.encode(valid, KEY, headers={"kid": "k1"})),
        ("valid float exp", raw_token(hs256, {"sub": "alice", "exp": FAR + 0.5})),
        ("valid extra claims", jwt.encode({**valid, "role": "x", "iss": "y"}, KEY)),
        ("valid past nbf", jwt.encode({**valid, "nbf": PAST}, KEY)),
        ("valid empty aud", raw_token(hs256, {**valid, "aud": ""})),
        ("valid string jti", jwt.encode({**valid, "jti": "id"}, KEY)),
        ("valid unicode subject", jwt.encode({"sub": "élise", "exp": FAR}, KEY)),
        ("expired", jwt.encode({"sub": "alice", "exp": PAST}, KEY)),
        ("other key", jwt.encode(valid, OTHER_KEY)),
        ("no exp", jwt.encode({"sub": "alice"}, KEY)),
        ("null exp", raw_token(hs256, {"sub": "alice", "exp": None})),
        ("no sub", jwt.encode({"exp": FAR}, KEY)),
        ("null sub", raw_token(hs256, {"sub": None, "exp": FAR})),
        ("empty sub", jwt.encode({"sub": "", "exp": FAR}, KEY)),
        ("int sub", raw_token(hs256, {"sub": 1, "exp": FAR})),
        ("list sub", raw_token(hs256, {"sub": ["a"], "exp": FAR})),
        ("string exp", raw_token(hs256, {"sub": "alice", "exp": str(FAR)})),
        ("string float exp", raw_token(hs256, {"sub": "alice", "exp": f"{FAR}.5"})),
        ("string expired exp", raw_token(hs256, {"sub": "alice", "exp": str(PAST)})),
        ("huge exp", raw_token(hs256, '{"sub":"alice","exp":1e400}')),
        ("far exp", raw_token(hs256, '{"sub":"alice","exp":1e300}')),
        ("b64 true header", raw_token({**hs256, "b64": True}, valid)),
        ("nbf string", raw_token(hs256, {**valid, "nbf": "x"})),
        ("bool exp", raw_token(hs256, {"sub": "alice", "exp": True})),
        ("list exp", raw_token(hs256, {"sub": "alice", "exp": [FAR]})),
        ("future iat", jwt.encode({**valid, "iat": FAR - 100}, KEY)),
        ("string iat", raw_token(hs256, {**valid, "iat": "x"})),
        ("future nbf", jwt.encode({**valid, "nbf": FAR - 100}, KEY)),
        ("aud", jwt.encode({**valid, "aud": "x"}, KEY)),
        ("int jti", raw_token(hs256, {**valid, "jti": 1})),
        ("int kid", raw_token({**hs256, "kid": 1}, valid)),
        ("crit", raw_token({**hs256, "crit": ["exp"]}, valid)),
        ("alg none", jwt.encode(valid, None, algorithm="none")),
        ("alg HS512", jwt.encode(valid, KEY * 2, algorithm="HS512")),
        ("alg hs256 lower case", raw_token({"alg": "hs256"}, valid)),
        ("no alg", raw_token({"typ": "JWT"}, valid)),
        ("header not an object", raw_token("[1]", valid)),
        ("payload not an object", raw_token(hs256, "[1]")),
        ("payload not JSON", raw_token(hs256, "{")),
        ("empty", ""),
        ("garbage", "garbage"),
        ("two segments", f"{h}.{p}"),
        ("four segments", f"{h}.{p}.{s}.{s}"),
        ("empty signature", f"{h}.{p}."),
        ("truncated signature", f"{h}.{p}.{s[:-2]}"),
        ("signature of another payload", f"{h}.{b64(json.dumps({'sub': 'admin', 'exp': FAR}).encode())}.{s}"),
        ("padded segments", f"{h}=.{p}.{s}"),
    ]


def verdict(token: str) -> str | None:
    try:
        return decode_access_token(token, KEY)
    except jwt.InvalidTokenError:
        return None


def check() -> None:
    doc = json.loads((HERE / "go_tokens.json").read_text(encoding="utf-8"))
    for item in doc["tokens"]:
        sub = decode_access_token(item["token"], doc["key"])
        payload = jwt.decode(item["token"], doc["key"], algorithms=["HS256"])
        assert sub == item["sub"], (sub, item)
        assert set(payload) == {"sub", "iat", "exp"}, payload
        assert payload["exp"] - payload["iat"] == item["ttl_seconds"], payload
    print(f"PyJWT {jwt.__version__}: {len(doc['tokens'])} Go tokens verified")


def main() -> None:
    if "--check" in sys.argv:
        check()
        return
    doc = {
        "key": KEY,
        "issued": issued(),
        "decoded": [{"name": n, "token": t, "subject": verdict(t)} for n, t in candidates()],
    }
    (HERE / "jwt_vectors.json").write_text(
        json.dumps(doc, indent=1, ensure_ascii=False) + "\n", encoding="utf-8"
    )


if __name__ == "__main__":
    main()
