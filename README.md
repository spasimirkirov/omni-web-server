<p align="center">
  <img src="favicons/android-chrome-192x192.png" width="96" alt="Omni Web Server logo">
</p>

<h1 align="center">Omni Web Server</h1>

<p align="center">
  A local PHP development server for Windows: Apache, several PHP versions side by side, and MySQL or MariaDB.<br>
  It has a web dashboard, automatic <code>*.test</code> sites and one-click HTTPS.
</p>

> [!NOTE]
> **Made with Claude Code.** Omni Web Server is created by
> [@spasimirkirov](https://github.com/spasimirkirov), who designs the product
> and its architecture, and developed with
> [Claude Code](https://claude.com/claude-code), Anthropic's AI coding agent,
> which writes the code. The project is also a test of how far that way of
> working goes for a real developer tool.

<p align="center">
  <img src="https://img.shields.io/badge/status-in%20development-orange" alt="Status: in development">
  <img src="https://img.shields.io/badge/Windows-10%20%7C%2011-0078D6?logo=windows" alt="Windows 10 and 11">
  <img src="https://img.shields.io/badge/Go-1.22%2B-00ADD8?logo=go&logoColor=white" alt="Go 1.22+">
  <a href="LICENSE"><img src="https://img.shields.io/badge/license-MIT-green" alt="MIT License"></a>
</p>

---

**Contents:**
[Features](#features) ·
[Install](#install) ·
[Guides](#guides) ·
[Command line](#command-line) ·
[Folder layout](#folder-layout) ·
[How it works](#how-it-works) ·
[Troubleshooting](#troubleshooting) ·
[Importing existing projects](#importing-existing-projects) ·
[Building from source](#building-from-source)

You control everything from a dashboard at <http://localhost:8800> and from
the tray icon next to the clock. A single `omni.exe` manages everything, and an
installer sets it up.

## Features

| | |
|---|---|
| **Automatic sites** | Every folder in `www` becomes `http://<folder>.test`. Each project has an on/off switch. |
| **New project from a template** | **New project…** creates a ready-to-use site in `www`: Laravel (also with the React, Vue or Livewire starter kit), WordPress, Symfony, plain PHP or static HTML. Pick one of the five newest versions, the PHP version and a database name. Omni Web Server creates the database, fills in `.env` or `wp-config.php`, runs the migrations or the WordPress installer, and builds the front end. |
| **Framework detection** | The right document root is picked automatically for Laravel, Symfony, CodeIgniter 4 (`public/`), Yii and Craft (`web/`), and WordPress. You can override it. |
| **PHP per project** | Install any number of PHP versions and choose one per project. Toggle extensions and edit `php.ini` per version. **Xdebug** is one click away, and a small list of PECL extensions (redis, apcu, igbinary, xdebug, mongodb) can be downloaded for a PHP version with "Download and enable". When a newer patch of an installed PHP exists, the PHP tab marks it with an **Update** button; nothing is ever installed automatically. Installing a PHP build brings over the PECL extensions that were switched on in the build it replaces (or, for a new version, in the default PHP). Progress of installs and updates is on the **Activity** tab. |
| **Databases** | MySQL 8.4 or MariaDB 11.4, ready to use with any client (HeidiSQL, DBeaver, PhpStorm). Adminer is optional. |
| **Mail and cache** | **Mailpit** catches every e-mail your apps send. **Redis** handles cache, queues and sessions (the PHP extension for `REDIS_CLIENT=phpredis` is one click away in the PHP tab). |
| **Composer, Node.js and Python** | Composer comes with the server. Node.js and Python install from the **Tools** tab with one click, system-wide like any program, so projects and virtual environments keep working even without Omni Web Server. The Tools tab shows the installed version of each winget app (one cached `winget list` call, refreshed every few minutes) and an **Update** button when winget knows a newer one; Composer shows its latest stable version and says **Up to date** instead of downloading again. winget tasks stop after 20 minutes with a hint to check for a UAC prompt. |
| **Same PHP everywhere** | `php`, `composer`, `mysql` and other commands are added to your **PATH**. Terminals, VS Code and PhpStorm use the same PHP and `php.ini` as the web server. |
| **Wildcard DNS and HTTPS** | Subdomains such as `api.blog.test` work. One click gives you `https://` with a locally trusted CA. |
| **Share a site** | One click gives a public `https://….trycloudflare.com` URL for webhooks or showing a client. |
| **Developer apps** | Git, VS Code, PhpStorm, HeidiSQL, DBeaver, Windows Terminal, Docker Desktop and others install through winget. |
| **Tray icon** | Right-click the icon next to the clock to open the dashboard, start, stop or restart services, open the projects or logs folder, or quit. Double-click opens the dashboard. |
| **Update check** | Once a day it asks GitHub for a newer version and shows a banner in the dashboard and an entry in the tray menu. You download and run the new installer yourself; settings and data are kept. Turn it off under **Settings → Updates**. |
| **Tidy** | It manages the hosts file for you and restarts services that crash. Nothing keeps running after you quit. Uninstalling undoes every system change. |

## Install

> Omni Web Server is still in development. Releases are marked as
> pre-releases until it is stable.

1. Download `OmniWebServer-<version>-Setup.exe` from
   [Releases](https://github.com/spasimirkirov/omni-web-server/releases) and
   run it.
2. **Program folder**: for example `C:\OmniWebServer`. Use a short path
   without spaces. Avoid *Program Files*, because normal users can't write
   to it.
3. **Projects folder (www)**: anywhere, for example `D:\Projects`. The
   uninstaller never deletes it.
4. **Components**: Apache, PHP, MySQL, Composer and Mailpit. You
   can pick MariaDB instead of MySQL. If the Visual C++ Redistributable that
   Apache, PHP and MySQL need is missing, the installer adds it. Extras such
   as Redis, Adminer, Xdebug, Node.js, Python and developer apps (Git, VS Code,
   HeidiSQL, ...)
   are installed later from the dashboard.
5. Click **Finish**. Omni Web Server starts and downloads the selected
   components. You can watch the progress at the top of the dashboard. Then
   it starts everything. A sample site is at <http://welcome.test>, and the
   tray icon next to the clock opens the dashboard at any time.

**Run as administrator** is on by default. It makes the shortcuts start
elevated, so changes to the hosts file and DNS need no UAC prompts.
**Start with Windows** adds a sign-in entry that starts Omni Web Server in the
background.

For a silent install, run
`OmniWebServer-<version>-Setup.exe /S /WWW=D:\Projects /D=C:\OmniWebServer`.
There is also a **portable zip**: unzip it anywhere and run `omni.exe`.

When you uninstall, it stops everything and removes the hosts entries, the DNS
rule, the PATH entry and the registry key. It then asks whether to also delete
databases, downloaded components and settings. Your projects are always kept.

## Guides

### A new project in a minute

On the **Overview** tab, click **New project…** and choose a template, a version, the PHP version and a database name. For WordPress you also enter the site title and the admin account. Progress appears at the top of the dashboard, and the full output goes to the `projects` log. When it finishes, the site is live at `http://<name>.test`.

### A project from a Git repository

In the same **New project** panel choose **Source: Git repository**. Enter the repository URL (`https://github.com/user/repo.git` or `git@github.com:user/repo.git`), a folder name (filled in from the repository name), and optionally a branch or tag and a shallow clone (`--depth 1`: faster, but no full history). After the clone you can tick, one by one: `composer install`, copy `.env.example` to `.env` plus `php artisan key:generate`, `npm install`, and a MySQL database (its name goes into `.env`). Nothing runs unless ticked, and a step whose file is missing (for example no `composer.json`) is skipped. The clone is made in a hidden folder and moved into `www` only when everything worked, so a failure leaves nothing behind, and a folder that already existed is never touched.

Git is started without a shell, never asks for a password (a private repository fails at once: set up a credential helper or an SSH key first) and accepts only `https://`, `http://`, `ssh://`, `file://` and `git@host:path` URLs. Git itself must be installed; the panel offers **Install Git** otherwise. From the command line:

```text
omni.exe new blog --git https://github.com/user/blog.git --branch main --depth 1 --composer --env --npm --db
```

### Laravel by hand

```bat
cd D:\Projects
composer create-project laravel/laravel blog
:: http://blog.test  (DocumentRoot = blog\public)
```

The new project uses SQLite by default. To use MySQL instead, create a
database named `blog` with any MySQL client (HeidiSQL, DBeaver or Adminer from
the **Tools** tab), then set these values in `.env`:

```dotenv
DB_CONNECTION=mysql
DB_HOST=127.0.0.1
DB_DATABASE=blog
DB_USERNAME=root
DB_PASSWORD=
MAIL_HOST=127.0.0.1
MAIL_PORT=1025
REDIS_HOST=127.0.0.1
```

Then run `npm install && npm run dev` (install Node.js from the **Tools** tab first). Mailpit's
inbox is at <http://localhost:8025>.

### Debugging with Xdebug

1. In the **PHP** tab, click **Install Xdebug** for your PHP version. It is set
   up with `xdebug.mode=debug,develop`, `start_with_request=trigger` and port
   9003.
2. Start listening in VS Code (with the PHP Debug extension) or in PhpStorm.
3. Add `?XDEBUG_TRIGGER=1` to the URL, or use the Xdebug helper extension in
   your browser.

### Wildcard DNS

Turn it on under **Settings → Wildcard DNS**. Accept the one-time UAC prompt, which installs the NRPT rule. You can check it from
PowerShell:

```powershell
Get-DnsClientNrptRule | Where-Object Comment -eq 'OmniWebServer'   # Namespace .test -> 127.0.0.1
Resolve-DnsName anything.blog.test                                 # 127.0.0.1 / ::1
```

- **Port 53 must be free.** Windows sends NRPT queries only to port 53. If
  another program already uses `127.0.0.1:53` (for example Internet
  Connection Sharing), Omni Web Server shows which one. Your sites keep
  working through the hosts file.
- **`.test` and `.local`.** New installs use `.test`, which is reserved for
  testing and resolves fast. Installs made before this default keep `.local`
  until you change it. `.local` is used by mDNS on Windows and can be slow for
  names that are missing from the hosts file. Change the suffix under
  **Settings → Domain suffix**: one click updates the vhosts, the hosts file,
  the HTTPS certificates, the wildcard DNS rule and restarts Apache, with a
  progress list that tells you what to do if a step needs administrator
  rights. Omni Web Server does not edit your projects, so update bookmarks and
  `APP_URL` in each project's `.env` by hand.
- **Browsers with their own secure DNS.** A browser that uses DNS-over-HTTPS
  may bypass the system resolver. Chrome and Edge fall back to the system
  resolver when NRPT rules exist. In Firefox, add the suffix to
  `network.trr.excluded-domains`, or turn off DNS-over-HTTPS.
- **Turning it off** removes the NRPT rule and restores the hosts entries.
- **Linux and macOS.** Omni Web Server runs the DNS server but doesn't change
  the system resolver. Point dnsmasq (`server=/test/127.0.0.1`) or
  systemd-resolved at it yourself.

### HTTPS

Turn it on under **Settings → HTTPS**. Then click **Trust certificate** and
confirm the Windows dialog. Every enabled site now also answers on `https://` with a valid
certificate. This includes one level of subdomains, such as
`https://api.blog.test`. Plain `http://` keeps working.

- **Laravel:** set `APP_URL=https://blog.test`. PHP receives `HTTPS=on`, so
  `url()` and `asset()` generate `https://` links.
- **Phones and other devices:** click **Download CA** and install the
  certificate on the device.
- **The CA's private key** is `etc\ssl\omni-ca.key`. Anyone who has it can
  create certificates that your PC trusts, so never share or commit it.

  To replace the CA:
  1. Delete `etc\ssl\omni-ca.*`.
  2. Trust the new CA.
  3. Remove the old one in `certmgr.msc` → *Trusted Root Certification
     Authorities*.

## Command line

You don't need the command line: the dashboard and the tray icon do
everything. For scripts, `omni.exe` in the install folder has the same
features. It is deliberately not added to PATH, so it can't clash with another
program's `omni` command. Run it with its full path, for example
`C:\OmniWebServer\omni.exe status`. Most commands talk to the running server.

```text
omni.exe help                          every command
omni.exe start | stop | quit           start in background + services | stop services | exit
omni.exe status                        services, PHP pools and sites
omni.exe new blog --git <url> [--branch x] [--depth 1] [--composer] [--env] [--npm] [--db]   clone a repository into a project
omni.exe site enable|disable blog      toggle a vhost
omni.exe site php blog 8.2             PHP version per project ("default" to reset)
omni.exe site root blog web            custom document root ("auto" to reset)
omni.exe site add shop D:\work\shop    vhost for a folder outside www
omni.exe php install 8.4               download PHP (or 7.4.33 from the archives)
omni.exe ext 8.4 on intl               toggle extensions
omni.exe install xdebug 8.4            Xdebug for PHP 8.4
omni.exe ext 8.4 pecl                  PECL extensions with a build for PHP 8.4
omni.exe ext 8.4 install redis         download php_redis.dll and enable it (--no-enable: only download)
omni.exe install phpredis [8.4]        same as ext 8.4 install redis
omni.exe install composer | mailpit | redis | mariadb | adminer
omni.exe install tool node             Node.js, Python, Git, ... via winget (list: omni.exe tools)
omni.exe install toolupdate git       update a winget app (omni.exe tools shows which have updates)
omni.exe db create blog                utf8mb4 database
omni.exe dns on | ssl on | ssl trust   wildcard DNS, HTTPS, trust the local CA
omni.exe share blog [off]              public https URL through Cloudflare
omni.exe logs apache_error 100         show a log
omni.exe migrate laragon | xampp       import from Laragon/XAMPP (lists the steps first)
```

## Folder layout

```text
C:\OmniWebServer\
  omni.exe                  dashboard, CLI and supervisor
  omniw.exe                 the same without a console window (shortcuts, autostart)
  omni.json                 settings and per-project choices
  bin\apache\ bin\php\php-8.4.x\ bin\mysql\ bin\redis\ bin\mailpit\ bin\composer\
  bin\shims\                php, composer, mysql, redis-cli ... (on PATH)
  etc\apache\httpd.conf     generated; your own directives go in etc\apache\custom\*.conf
  etc\php\8.4\php.ini       generated per version from php.ini-development + your settings
  etc\mysql\my.ini          generated; overrides go in etc\mysql\custom.ini
  etc\redis\redis.conf      generated; overrides go in etc\redis\custom.conf
  etc\ssl\                  local CA, per-site certificates, CA bundle for PHP
  data\mysql\ data\redis\ data\mailpit\
  logs\                     apache_error, mysql_error, php84_error, import, omni, ...
D:\Projects\                your projects (www), wherever you chose
```

Other programs can find Omni Web Server through the registry key
`HKCU\Software\OmniWebServer`. It holds the install folder, the www folder and
the paths of every installed component. The `%OMNI_WEB_SERVER_HOME%` environment
variable points to the install folder.

## How it works

- **Apache** runs from a generated `httpd.conf`:
  - There is one `<VirtualHost>` per enabled project, with `AllowOverride All`,
    so `.htaccess` files such as Laravel's rewrite rules work.
  - Requests for `.php` files that exist go to PHP over FastCGI through
    `SetHandler "proxy:fcgi://…/"`.
  - Dotfiles such as `.env` and `.git` are denied.
- **PHP** runs as a pool of `php-cgi.exe -b` workers per version, 8 by
  default for new versions (older installs keep their number).
  - On Windows, php-cgi serves only one request at a time. Omni Web Server
    listens on one port per version (`9000 + major*100 + minor*10`, so 9840
    for PHP 8.4), hands each request to an idle worker and queues the rest.
  - As a result, each version handles several requests at once, and versions
    are fully isolated from each other.
- **A watcher** rescans `www` every 3 seconds. It rewrites the configuration
  only when something has changed, and restarts Apache only after
  `httpd -t` accepts the new configuration.
- **The dashboard** listens only on `127.0.0.1`. It rejects requests with a
  foreign `Host` header, to block DNS rebinding. It also rejects
  state-changing requests that don't carry its own header, to block
  cross-site requests.

## Performance for Laravel

Everything below is in **Settings → Performance** or on the **PHP** tab. Each
setting shows what you gain and what you pay. Defaults are the safe choice;
nothing is locked, and your own `php.ini` values in the PHP tab always win.

| Setting | Default | Plus | Minus |
|---|---|---|---|
| Code changes visible | within 2 s | Much faster on big apps (OPcache checks files every 2 s, not on every request). | An edit can take up to 2 s to show. Choose "instantly" to always see fresh code. |
| OPcache size and path cache | 256 MB OPcache, 32 MB interned strings, 32531 files, 16M path cache for 10 min | Big vendor folders fit in the cache. | After changing a symlink or junction, PHP can use the old path for up to 10 minutes (restart the workers). |
| OPcache for command line | on | `php artisan`, composer and PHPUnit reuse compiled files (kept in `tmp\opcache`). Only the `php` and `composer` shims use it. | The cache folder grows; very deep vendor trees can hit the 260 character path limit. |
| Workers per PHP version | 8 for new versions (existing installs keep their number); up to 32 | Pages load in parallel; a page that calls its own site over HTTP needs two workers. | Each idle worker holds memory. The PHP card shows busy and waiting requests and a recommendation for your CPU count. |
| Requests per worker | 1000 | A worker restarts after N requests, which clears memory leaks. | A restart costs a few ms every N requests (0 = never). |
| Xdebug debug mode | off | PHP, artisan and tests run at normal speed with the extension loaded. | Turn it to debug (or develop, coverage) before you debug. |
| Skip .htaccess (per site) | off | Laravel, Symfony and CodeIgniter sites use the rewrite in the vhost; Apache stops looking for `.htaccess` on every request. | Rules in the project's `.htaccess` are ignored. |
| Fast dev database | off | No binary log, no disk flush on every commit, bigger buffer pool (default 512M): faster migrations, seeders and tests. | If Windows crashes, up to 1 second of writes can be lost. Restart MySQL after changing it. |
| Redis persistence | off | No snapshot stalls (the Windows build emulates fork). | Cache, sessions and queued jobs are gone after a restart. |
| Redis memory limit | 512mb, `volatile-lru` | A runaway cache cannot fill the RAM; only keys with an expiry are evicted, so queued jobs stay. | Old cache entries disappear at the limit. |
| HTTP/2 on HTTPS sites | on | Faster asset loading. | Needs mod_http2; slightly different timing for HTTP/1.1 tools. |
| Log static files | off | `apache_access.log` stays small. | Missing-asset debugging needs the setting on. |

Other things Omni Web Server does by itself: Apache keep-alive allows 500
requests per connection, and `logs\apache_access.log` is rotated at 10 MB
(3 old files, `.1` to `.3`). `EnableSendfile` stays off because it can
misbehave on network and VM-shared drives.

**Windows Defender.** The biggest win on Windows is to stop Defender scanning
every file PHP, composer and npm touch. Settings → Performance → **Speed up
(Windows Defender)** adds exclusions for the `bin`, `data`, `tmp` and projects
folders and for php-cgi, php, httpd, mysqld, mariadbd, node and redis-server.
Windows asks for administrator rights, nothing is applied automatically, and
**Remove exclusions** undoes it. The folders are then no longer scanned for
viruses, so only use packages you trust. Without administrator rights Windows
hides the exclusions, and the status reads "unknown".

**Also worth doing**

- Keep the projects folder on a local NTFS drive, not on a network share, WSL
  path or OneDrive.
- Horizon needs `pcntl`, which does not exist on Windows. Use
  `php artisan queue:work --max-time=3600 --memory=256` and run
  `php artisan queue:restart` after code changes. Octane with Swoole does not
  run on Windows.
- `npm run dev` talks to the browser directly and does not go through Apache.
  On HTTPS sites point Vite at `etc\ssl\sites\<host>.crt` and `.key`.
- Use SQLite in memory or `RefreshDatabase` for tests, and
  `php artisan test --parallel`.

## Troubleshooting

| Problem | Fix |
|---|---|
| **Port 80 is already in use by PID 4 "System"** | IIS or the *World Wide Web Publishing Service* is running. Stop it in `services.msc`, or change the Apache port under **Settings**. |
| **Port 80 is already in use by another program** | Close that program, or change the port. |
| **Apache, PHP or MySQL exits immediately** | Install the [Visual C++ Redistributable](https://aka.ms/vs/17/release/vc_redist.x64.exe), then check the log that the dashboard shows. |
| **`name.test` doesn't resolve** | Click **Settings → Rewrite hosts file now**, or **Apply DNS rule** when wildcard DNS is on. Accept the UAC prompt, or run Omni Web Server as administrator. Then check that the hosts file has the Omni Web Server block, or that `Get-DnsClientNrptRule` lists the `OmniWebServer` rule. |
| **`Class "Redis" not found`** | The PHP Redis extension is missing for the PHP version of the project. Run `omni.exe ext 8.4 install redis` (or click **Add Redis extension to PHP 8.4** on the Redis card, or **Download and enable** under PECL extensions in the PHP tab), then check with `php -m`. After a PHP patch update an enabled extension can show as *missing*: click **Download again**. Alternatively set `REDIS_CLIENT=predis` in `.env`. |
| **Windows SmartScreen warns about `omni.exe`** | The build isn't code-signed. Choose **More info → Run anyway**. |

## Importing existing projects

If you already have projects in a local XAMPP or Laragon install, Omni Web
Server can import them from the **Import** tab of the dashboard. The tab
appears only when one of them is found. The installer also offers the import.

The import lists every step and asks before it starts. It does the following:
- installs the PHP version the existing install uses;
- copies the projects into your projects folder;
- dumps the databases with that install's own `mysqldump` and imports them;
- keeps the MySQL root password;
- stops the old servers so that ports 80 and 3306 are free.

Nothing is deleted. Details go to `logs\import.log`. You may need to recreate
symbolic links inside projects, for example with `php artisan storage:link`.

XAMPP and Laragon are the work of their own authors. Omni Web Server is not
affiliated with them and only reads their files to import your projects.

## Building from source

You need Go 1.22 or newer, which needs only the standard library, and NSIS 3
for the installer.

```sh
./build.sh
```

This runs the tests and builds `dist/OmniWebServer/omni.exe` and `omniw.exe`,
the Setup.exe and a portable zip.

On Windows:

1. Install the tools with `winget install GoLang.Go` and
   `winget install NSIS.NSIS`.
2. Open a new terminal.
3. Double-click `build.cmd`, which runs `build.sh` with Git Bash, or run
   `bash build.sh` from Git Bash.

To publish a release, set `appVersion` in `app.go`, then push a matching tag:
`git tag v1.2.0 && git push origin v1.2.0`. GitHub Actions
(`.github/workflows/build.yml`) then builds the same files and attaches them to
the release. **Actions → Build → Run workflow** builds them without a release.

`rsrc_windows_amd64.syso` embeds the icon and version information in
`omni.exe`. To regenerate it, run:

```sh
go run github.com/tc-hib/go-winres@v0.3.3 simply --arch amd64 --icon logo.png --manifest cli \
  --product-name "Omni Web Server" --product-version 1.2.0 --file-version 1.2.0.0 \
  --file-description "Omni Web Server - Apache, PHP, MySQL for Windows" \
  --copyright "Omni Web Server" --original-filename omni.exe
```

The favicons in `favicons/` and `web/` are generated from `logo.png` as well.
`installer/omni.nsi` is the installer script, in English and Bulgarian.

Omni Web Server also runs on Linux. On first run it finds the system's Apache,
`php-cgi*`, MySQL or MariaDB, Redis and Mailpit.

## License and credits

Omni Web Server is released under the [MIT License](LICENSE).
[CREDITS.md](CREDITS.md) lists the projects it installs and builds on, and
their licenses.
