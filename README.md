<p align="center"><img src="favicons/android-chrome-192x192.png" width="96" alt=""></p>

# Omni Web Server

A Laragon-style local web server for PHP development on Windows 10/11:
**Apache + several PHP versions side by side + MySQL/MariaDB**, plus Composer,
Node.js, Mailpit, Redis and Xdebug. It gives you automatic `*.local` virtual
hosts, wildcard DNS and one-click HTTPS, all controlled from a web dashboard
(<http://localhost:8800>) or the `omni` command line. A single `omni.exe`
manages everything, and an installer sets it up.

## Features

- **Installer.** You choose the program folder and, separately, the projects
  (`www`) folder, which can be on any drive. You also tick the components you
  want: they are downloaded on the first start, so the server is ready right
  away. Programs and Features gets an uninstaller.
- **Automatic vhosts.** Every folder in `www` becomes `http://<folder>.local`,
  with an on/off toggle per project. *Auto vhost* enables new folders
  immediately.
- **Framework detection.** Laravel (`artisan` or `laravel/framework`),
  Symfony, CodeIgniter 4 (`public/`), Yii/Craft (`web/`) and WordPress are
  detected, and you can override any document root.
- **Per-project PHP versions.** Install any number of PHP versions, choose
  one per project, and toggle extensions or edit `php.ini` per version.
  **Xdebug** installs with one click per version and is set up for VS Code
  and PhpStorm (port 9003).
- **MySQL 8.4 or MariaDB 11.4** with a database manager in the dashboard, plus
  Adminer.
- **Mailpit.** Catches every e-mail your apps send. `mail()` and Laravel's
  SMTP (`127.0.0.1:1025`) go to the inbox at <http://localhost:8025>.
- **Redis** for Laravel cache, queues and sessions (`127.0.0.1:6379`).
- **Composer and Node.js** (several versions, one default) without separate
  installers.
- **Discoverable everywhere.** `bin\shims` holds `php`, `php83`, `composer`,
  `node`, `npm`, `npx`, `mysql`, `mysqldump` and `redis-cli`, and it is
  added to your user **PATH**. Every terminal, PhpStorm and VS Code find the
  same PHP (with the same `php.ini`) as the web server. The registry key
  **`HKCU\Software\OmniWebServer`** records the install folder, the www
  folder and the paths of every PHP version, MySQL, Apache, Redis, Mailpit and
  Node.js. **`%OMNI_HOME%`** points to the install folder.
- **Wildcard DNS** (`api.blog.local`, `tenant1.shop.local`) and **one-click
  HTTPS** with a locally trusted CA.
- **Share a site.** One click gives a public `https://….trycloudflare.com`
  URL for webhooks or showing a client (needs Cloudflare Tunnel).
- **Developer apps through winget**: Git, Windows Terminal, VS Code,
  PhpStorm, HeidiSQL, DBeaver, Cloudflare Tunnel, ngrok, Docker Desktop and
  WSL. Each can be installed and its status checked from the Tools tab.
- **hosts file managed for you**, process supervision with automatic restarts,
  and no orphaned processes when Omni Web Server closes.

## Install

1. Run `OmniWebServer-<version>-Setup.exe`.
2. **Program folder**: e.g. `C:\OmniWebServer`. Use a short path without
   spaces, and avoid *Program Files*, which normal users can't write to.
3. **Projects folder (www)**: anywhere, e.g. `D:\Projects`. The uninstaller
   never deletes it.
4. **Components**: Apache, PHP, MySQL, Composer, Node.js and Mailpit are
   selected by default. Redis, MariaDB, Adminer and the winget apps (Git is on
   by default) are optional. The Visual C++ Redistributable that
   Apache/PHP/MySQL need is installed when missing.
5. Finish. Omni Web Server starts, downloads the selected components (watch
   the progress at the top of the dashboard), then starts everything. A
   sample site is at <http://welcome.local>.

"Run as administrator" (on by default) marks the shortcuts to start elevated,
so hosts/DNS changes need no UAC prompts. "Start with Windows" adds a sign-in
entry that starts Omni Web Server in the background.

Silent install:
`OmniWebServer-1.1.0-Setup.exe /S /WWW=D:\Projects /D=C:\OmniWebServer`.
A **portable zip** is also built: unzip `omni.exe` anywhere and run it.

Uninstalling stops everything and removes the hosts entries, the DNS rule,
the PATH entry and the registry key. It then asks whether to delete databases,
downloaded components and settings too. Projects are always kept.

### Laravel in 30 seconds

```bat
cd D:\Projects
composer create-project laravel/laravel blog
omni db create blog
:: http://blog.local  (DocumentRoot = blog\public)
```

`.env`: `DB_HOST=127.0.0.1`, `DB_USERNAME=root`, `DB_PASSWORD=`,
`MAIL_HOST=127.0.0.1`, `MAIL_PORT=1025`, `REDIS_HOST=127.0.0.1`.
Run `npm install && npm run dev` with the bundled Node.js.

### Debugging with Xdebug

PHP tab → **Install Xdebug** for the version. It is configured with
`xdebug.mode=debug,develop`, `start_with_request=trigger` and port 9003.
Start listening in VS Code (PHP Debug extension) or PhpStorm, then add
`?XDEBUG_TRIGGER=1` to the URL or use the browser's Xdebug helper.

### Wildcard DNS

Turn it on under **Settings → Wildcard DNS** or with `omni dns on`.
Accept the one-time UAC prompt, which installs the NRPT rule. Check it from
PowerShell:

```powershell
Get-DnsClientNrptRule | Where-Object Comment -eq Omni Web Server   # Namespace .local -> 127.0.0.1
Resolve-DnsName anything.blog.local                         # 127.0.0.1 / ::1
```

Notes:
- Windows can only send NRPT queries to port 53. If another program (e.g.
  Internet Connection Sharing) already uses `127.0.0.1:53`, Omni Web Server shows
  that program. In that case sites keep working through the hosts file.
- `.local` is also used by mDNS (printers, network devices). If your network
  has `.local` devices, switch the domain suffix to `.test`, which is reserved
  for exactly this purpose; the rule follows automatically.
- Browsers using their own "secure DNS" (DNS-over-HTTPS) may bypass the
  system resolver. Chrome and Edge fall back to the system resolver when NRPT
  rules exist. In Firefox, add the suffix to
  `network.trr.excluded-domains` or turn off DoH.
- Turning wildcard DNS off removes the NRPT rule and restores the hosts
  entries. The rule stays in place while Omni Web Server is closed. Apache is down
  then anyway, so nothing else is affected.
- On Linux/macOS, Omni Web Server runs the DNS server but doesn't change the system
  resolver. Point dnsmasq (`server=/local/127.0.0.1`) or systemd-resolved at
  it yourself.

### HTTPS

Turn it on under **Settings → HTTPS** (or `omni ssl on`), then click
**Trust certificate** (`omni ssl trust`) and confirm the Windows dialog.
Every enabled site now also answers on `https://` with a valid certificate,
including one level of subdomains (`https://api.blog.local`). Plain `http://`
keeps working.

- Laravel: set `APP_URL=https://blog.local`. PHP receives `HTTPS=on`, so
  `url()` and `asset()` generate `https://` links.
- Phones and other devices: **Download CA** and install it there.
- The CA's private key is `etc\ssl\omni-ca.key`. Anyone holding it can
  create certificates your PC trusts, so never share or commit it. To rotate
  it, delete `etc\ssl\omni-ca.*`, then trust the new CA. Remove the old
  one from `certmgr.msc` → Trusted Root Certification Authorities.

## Command line

`omni help` lists everything. Most commands talk to the running server (start
it with `omni start` or the shortcut).

```text
omni start | stop | quit           start in background + services | stop services | exit
omni status                        services, PHP pools and sites
omni site enable|disable blog      toggle a vhost
omni site php blog 8.2             PHP version per project ("default" to reset)
omni site root blog web            custom document root ("auto" to reset)
omni site add shop D:\work\shop    vhost for a folder outside www
omni php install 8.4               download PHP (or 7.4.33 from the archives)
omni ext 8.4 on intl               toggle extensions
omni install xdebug 8.4            Xdebug for PHP 8.4
omni install composer | node 22 | mailpit | redis | mariadb | adminer
omni install tool git              Windows apps via winget (see: omni tools)
omni node default 22.11.0          default Node.js
omni db create blog                utf8mb4 database
omni dns on | ssl on | ssl trust   wildcard DNS, HTTPS, trust the local CA
omni share blog [off]              public https URL through Cloudflare
omni logs apache_error 100         show a log
omni init --www D:\Projects         prepare settings (used by the installer)
omni cleanup                       undo hosts/DNS/PATH/registry (used by the uninstaller)
```

## Folder layout

```text
C:\OmniWebServer\
  omni.exe            dashboard, CLI and supervisor
  omni.json           settings and per-project choices
  bin\apache\ bin\php\php-8.4.x\ bin\mysql\ bin\node\ bin\redis\ bin\mailpit\ bin\composer\
  bin\shims\          php, composer, node, npm, mysql, redis-cli ... (on PATH)
  etc\apache\httpd.conf     generated; own directives go in etc\apache\custom\*.conf
  etc\php\8.4\php.ini       generated per version from php.ini-development + your settings
  etc\mysql\my.ini          generated; overrides in etc\mysql\custom.ini
  etc\redis\redis.conf      generated; overrides in etc\redis\custom.conf
  etc\ssl\             local CA, per-site certificates, CA bundle for PHP
  data\mysql\ data\redis\ data\mailpit\
  logs\                apache_error, mysql_error, php84_error, mailpit, redis, omni, ...
D:\Projects\           your projects (www), wherever you chose
```

## How it works

- **Apache** runs from a generated `httpd.conf` with one `<VirtualHost>` per
  enabled project and `AllowOverride All`, so `.htaccess` files (Laravel's
  rewrite rules) work. Requests for `.php` files that exist are handed to
  PHP over FastCGI with `SetHandler "proxy:fcgi://…"`. Dotfiles such as
  `.env` and `.git` are denied.
- **PHP** runs as a pool of `php-cgi.exe -b` workers per version (4 by
  default). php-cgi on Windows serves one request at a time, so Omni Web Server
  listens on one port per version (`9000 + major*100 + minor*10`, e.g. 9840
  for 8.4). It hands each request to an idle worker and queues the rest.
  Each version can therefore run several requests at once, and versions are
  fully isolated from each other.
- A watcher rescans `www` every 3 seconds. It rewrites configuration only when
  something changed, and restarts Apache only after `httpd -t` accepts the new
  config.
- The dashboard listens on `127.0.0.1` only. It rejects requests with a
  foreign `Host` header (DNS rebinding) and state-changing requests without
  its own header (cross-site requests).

## Troubleshooting

- **"Port 80 is already in use by PID 4 System"**: IIS or the *World Wide Web
  Publishing Service* is running. Stop it in `services.msc`, or change the
  Apache port under Settings.
- **"port 80 is already in use by … Skype/other"**: close that program or
  change the port.
- **Apache/PHP/MySQL exits immediately**: install the VC++ Redistributable
  (step 1), then check the log the dashboard shows.
- **`name.local` doesn't resolve**: click *Settings → Rewrite hosts file now*
  (or *Apply DNS rule* when wildcard DNS is on) and accept the UAC prompt,
  or run Omni Web Server as administrator. Then check that the Omni Web Server block exists
  in the hosts file, or that `Get-DnsClientNrptRule` lists the Omni Web Server rule.
- **Windows SmartScreen warns about omni.exe**: the build is not
  code-signed. Choose *More info → Run anyway*.

## Building from source

Go 1.22+ (standard library only) and NSIS 3 for the installer:

```sh
./build.sh      # tests, dist/OmniWebServer/omni.exe, the Setup.exe and a portable zip
```

`rsrc_windows_amd64.syso` embeds the icon and version info in `omni.exe`.
Regenerate it with
`go run github.com/tc-hib/go-winres@v0.3.3 simply --icon icon.png --manifest cli ...`.
`installer/omni.nsi` is the installer script (English and Bulgarian).

Omni Web Server also runs on Linux. On first run it detects the system Apache,
`php-cgi*`, MySQL/MariaDB, Redis and Mailpit; the end-to-end tests were run
that way.
