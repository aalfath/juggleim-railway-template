[![Deploy on Railway](https://railway.com/button.svg)](https://railway.com/new/template/juggleim?utm_medium=integration&utm_source=button&utm_campaign=juggleim)

# JuggleIM on Railway

A Railway template for [JuggleIM](https://github.com/juggleim/im-server) (`juggleim/im-server`), a self-hosted, Apache-2.0 instant-messaging server written in Go: private chat, groups, chatrooms, message history, a WebSocket endpoint for the client SDKs, a signed server REST API and a web admin console.

**Version:** JuggleIM v1.8.16 (admin console v1.0.9), built from the release source tarball (SHA-256 checked) on `golang:1.25.14-alpine@sha256:1ae0735f…`, runtime `alpine:3.22@sha256:49340b34…`. Upstream publishes no container image; its own Dockerfile compiles from source, and so does this one.

## What it deploys

| Service | Source | Public | Volume |
| --- | --- | --- | --- |
| `JuggleIM` | this repo (`Dockerfile`) | yes, one HTTPS domain | `/opt/data` (local KV store and message logs) |
| `MySQL` | Railway's MySQL (`mysql:8`) | no | `/var/lib/mysql` (users, messages, apps, console accounts) |

The JuggleIM server listens on four internal ports. A small launcher (`launcher/main.go`) puts them behind one public port, routed by path:

| Path | Goes to |
| --- | --- |
| `/im`, `/imbot` (WebSocket), `/health`, `/version` | connection manager |
| `/apigateway/…` | server API (signed with your app key and secret) |
| `/navigator/…` | SDK navigator (tells clients which WebSocket address to use) |
| everything else | admin console and `/admingateway/…` |

`/metrics` and the bundled JuggleChat demo API (`/jim/…`) are not exposed: the first has no login, and the demo's SMS and email login accepts a fixed code.

On every start the launcher:

1. writes the server config from the variables below;
2. loads `sql/imserver.sql` into the database if it is empty (later versions upgrade it themselves);
3. sets the console user `admin`'s password to `JUGGLEIM_ADMIN_PASSWORD` (upstream's default is `123456`; this template never uses it);
4. stores `wss://<your Railway domain>` as the address the navigator gives to client SDKs.

## Variables

| Variable | Default | Purpose |
| --- | --- | --- |
| `JUGGLEIM_ADMIN_PASSWORD` | generated | Password for the admin console user `admin`. Re-applied on every start, so change it here, not in the console |
| `JUGGLEIM_ADMIN_SECRET` | generated | Signs admin console sessions and internal admin calls |
| `MYSQLHOST`, `MYSQLPORT`, `MYSQLUSER`, `MYSQLPASSWORD`, `MYSQLDATABASE` | references to the `MySQL` service | Database connection |
| `PORT` | `8080` | Public port |
| `JUGGLEIM_CONNECT_ADDRESS` | not set | Optional. WebSocket address for SDKs if you add a custom domain, for example `wss://chat.example.com`. Defaults to `wss://` plus the Railway domain |

## First steps after deploying

1. Open the Railway domain, log in as `admin` with the `JUGGLEIM_ADMIN_PASSWORD` value from the service's Variables tab.
2. Create an app in the console. It gives you an **app key** and **app secret**.
3. From your backend, register users and get their tokens through the server API (`POST https://YOUR-DOMAIN/apigateway/users/register`, signed with the headers `appkey`, `nonce`, `timestamp` and `signature` = SHA-1 of app secret + nonce + timestamp). See upstream's [server API quick start](https://github.com/juggleim/im-server/blob/master/docs/server-api-quickstart.md).
4. Point a client SDK (Web, Android, iOS, Flutter, HarmonyOS) at `https://YOUR-DOMAIN` with the app key and the user's token. It asks the navigator for the WebSocket address and connects to `wss://YOUR-DOMAIN/im`.

The admin console is in Chinese by default.

## Updates

Railway tells everyone who deployed this template when a change is merged to this repo's `main` branch, and each deployer chooses when to apply it. Each change is listed in [CHANGELOG.md](CHANGELOG.md), breaking changes first. JuggleIM migrates its own MySQL schema on start.

## License

JuggleIM is Apache-2.0 (© the JuggleIM authors). The files in this repo are Apache-2.0 too. This is a community template, not affiliated with JuggleIM.
