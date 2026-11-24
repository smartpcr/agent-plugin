#!/usr/bin/env bash
# By-hand rows 4.8 (WSL ext4 and /mnt/e) and 4.9 (b) (WSL ext4, vim) at the frozen commit.
S=/mnt/c/Users/xiaodoli.REDMOND/AppData/Local/Temp/claude/095fd4c454d7f56c/32b1560e-0d49-424c-b2a5-d7e6081d5719/scratchpad/manual
O=$S/byhand-wsl-593ceaa.log
{
echo "commit: $(git -C ~/src/agent-plugin rev-parse HEAD) started: $(date -u +%FT%TZ)"
uname -r; grep PRETTY_NAME /etc/os-release; git --version
T=$(mktemp -d)
cp "$S/dsh-manual-list-linux" "$T/list"; cp "$S/dsh-manual-watch-linux" "$T/watch"; chmod +x "$T/list" "$T/watch"
compare() {
  local dir=$1; shift
  (cd "$dir" && git "$@" ls-files --cached --others --exclude-standard | LC_ALL=C sort -u) > "$T/git.txt"
  "$T/list" "$dir" 2> "$T/sum.txt" | LC_ALL=C sort -u > "$T/search.txt"
  echo "dir: $dir  git: $(wc -l < "$T/git.txt")  search: $(wc -l < "$T/search.txt")  $(cat "$T/sum.txt")"
  echo "only in git:"; LC_ALL=C comm -23 "$T/git.txt" "$T/search.txt" | head -20
  echo "only in search (top two levels):"
  LC_ALL=C comm -13 "$T/git.txt" "$T/search.txt" | cut -d/ -f1-2 | sort | uniq -c | sort -rn | head -20
}
echo "== row 4.8 WSL ext4"; compare ~/src/agent-plugin -c core.autocrlf=false
echo "== row 4.8 WSL /mnt/e"; compare /mnt/e/work/github/ai/agent-plugin -c core.filemode=false
echo "== row 4.9 (b) WSL ext4, vim"; vim --version | head -1
mkdir -p ~/dsh-watch; echo "{}" > ~/dsh-watch/settings.json
"$T/watch" ~/dsh-watch/settings.json 20 > "$T/w.out" 2>&1 &
wp=$!
for _ in $(seq 1 50); do grep -q '^watching' "$T/w.out" && break; sleep 0.2; done
for i in 1 2 3; do
  sleep 3
  vim -es -c 'normal Ax' -c wq ~/dsh-watch/settings.json
  echo "save $i at $(date -u +%T)"
done
wait "$wp"; cat "$T/w.out"
rm -rf ~/dsh-watch "$T"
echo "ended: $(date -u +%FT%TZ)"
} > "$O" 2>&1
cat "$O"
