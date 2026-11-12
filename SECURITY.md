# Security policy

## Supported versions

| Version | Supported |
| ------- | --------- |
| 2.x     | yes       |
| 1.x     | no — `POST /users` is unauthenticated in 1.x; upgrade to 2.x |

## Reporting a vulnerability

Please do not open a public issue. Report it privately through
[GitHub security advisories](https://github.com/MedAziz11/Uptime-Kuma-Web-API/security/advisories/new)
with the affected version, how to reproduce it and the impact.

## Deployment notes

- This API holds the credentials of an Uptime Kuma account and gives every API user full control
  over that Uptime Kuma instance. Do not expose it to untrusted networks; put it behind TLS.
- Use a strong `ADMIN_PASSWORD`. There is no rate limiting on `/login/access-token`; add it at your
  reverse proxy if the API is reachable by others.
- Set `SECRET_KEY` explicitly if several instances must accept the same tokens. Otherwise a key is
  generated and stored next to the database (`/db/secret_key`); protect that volume.
- Access tokens are valid for `ACCESS_TOKEN_EXPIRATION` minutes (8 days by default) and cannot be
  revoked individually; deleting the user or changing the signing key invalidates them.
