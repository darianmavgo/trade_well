package dashboard

// PageStyle is the stylesheet the operator pages share, for pages served outside this package.
func PageStyle() string { return pageStyle }

// pageStyle is the stylesheet shared by the read-only operator pages (/check_health,
// /manage_exit): light and dark, no dependencies.
const pageStyle = `
  :root { --bg:#fff; --fg:#1c1f24; --muted:#5b6470; --line:#dde1e6; --ok:#1a7f37; --warn:#9a6700; --bad:#c62828; --card:#f6f8fa; --code:#eef1f4; }
  @media (prefers-color-scheme: dark) {
    :root { --bg:#0f1216; --fg:#e6e9ed; --muted:#98a2b0; --line:#2a313a; --ok:#4cc26b; --warn:#e0b040; --bad:#ef6b6b; --card:#171b21; --code:#1f252d; }
  }
  body { margin:0; background:var(--bg); color:var(--fg); font:15px/1.55 -apple-system,BlinkMacSystemFont,"Segoe UI",Roboto,sans-serif; }
  main { max-width:1100px; margin:0 auto; padding:24px 16px 64px; }
  h1 { font-size:26px; margin:0 0 4px; }
  h2 { font-size:19px; margin:32px 0 10px; padding-bottom:6px; border-bottom:1px solid var(--line); }
  p.sub { color:var(--muted); margin:0 0 16px; }
  code { background:var(--code); padding:1px 5px; border-radius:4px; font-size:13px; }
  table { width:100%; border-collapse:collapse; margin:8px 0 4px; font-size:14px; }
  th, td { text-align:left; padding:7px 10px; border-bottom:1px solid var(--line); vertical-align:top; }
  th { color:var(--muted); font-weight:600; }
  .tag { display:inline-block; font-size:12px; font-weight:600; padding:1px 8px; border-radius:10px; border:1px solid currentColor; white-space:nowrap; }
  .ok { color:var(--ok); } .warn { color:var(--warn); } .bad { color:var(--bad); }
  .card { background:var(--card); border:1px solid var(--line); border-radius:8px; padding:12px 16px; margin:12px 0; }
  .muted { color:var(--muted); }
  .table-wrap { overflow-x:auto; }
  a { color:inherit; }
`
