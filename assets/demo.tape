# Regenerate with: vhs assets/demo.tape (from the repository root)
# Requires EDINET_API_KEY in the environment; it is inherited, never typed.

Output assets/demo.gif

Set Shell "bash"
Set FontSize 18
Set Width 1800
Set Height 540
Set Padding 24
Set Theme "Catppuccin Mocha"
Set TypingSpeed 45ms

Hide
Type `export D=/tmp/edinet-demo && rm -rf "$D" && mkdir -p "$D" && go build -o "$D/edinet" ./cmd/edinet && cd "$D" && export PATH="$D:$PATH" PS1='\[\e[1;35m\]$ \[\e[0m\]' && edinet company search 任天堂 >/dev/null 2>&1; clear`
Enter
Sleep 6s
Show

Type "edinet company search 任天堂"
Sleep 400ms
Enter
Sleep 2s

Type "edinet docs list --from 2026-06-20 --to 2026-06-30 --type yuho --company 任天堂"
Sleep 400ms
Enter
Sleep 6s

Type "clear"
Enter
Type "edinet facts S100Y9NX --grep 利益 --consolidated --period 当期 --numeric --limit 8"
Sleep 400ms
Enter
Sleep 4s

Type "edinet facts S100Y9NX --element NetSales --consolidated --period 当期 -o json | jq '.[0] | {filerName, secCode, accountingStandard, label, value}'"
Sleep 400ms
Enter
Sleep 5s
