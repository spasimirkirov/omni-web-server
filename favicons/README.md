# Favicons

Generated from `../logo.png`. Copy these files to the web root and add to `<head>`:

```html
<link rel="icon" href="/favicon.ico" sizes="any">
<link rel="icon" type="image/png" sizes="32x32" href="/favicon-32x32.png">
<link rel="icon" type="image/png" sizes="16x16" href="/favicon-16x16.png">
<link rel="apple-touch-icon" sizes="180x180" href="/apple-touch-icon.png">
<link rel="manifest" href="/site.webmanifest">
<meta name="msapplication-config" content="/browserconfig.xml">
<meta name="theme-color" content="#1e2a78">
```

| File | Size | Use |
|------|------|-----|
| favicon.ico | 16–256 px (7 sizes) | browsers, Windows shortcuts / .exe icon |
| favicon-16x16.png, -32x32, -48x48 | | browser tabs |
| apple-touch-icon.png | 180 px, white background | iOS home screen |
| android-chrome-192x192.png, -512x512 | | Android / PWA manifest |
| mstile-150x150.png | 150 px | Windows start tiles |
