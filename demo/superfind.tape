Output demo/superfind.gif

Set Shell zsh
Set FontSize 18
Set Width 1360
Set Height 560
Set Padding 18
Set Margin 3
Set MarginFill "#30363d"
Set Theme "GitHub Dark"
Set TypingSpeed 50ms
Set WaitTimeout 90s

Hide
Type `go build -o bin/superfind . && export DEMO_CLAUDE=$(command -v claude) DEMO_HOME=$HOME && export HOME=/tmp/sfd-demo XDG_CACHE_HOME=/tmp/sfd-demo/.cache PATH=$PWD/bin:/tmp/sfd-demo/bin:$PATH` Enter
Type `python3 demo/make-home.py $HOME && rehash && eval "$(superfind init zsh)" && cd ~/code && superfind index >/dev/null && { superfind label >/dev/null 2>&1; PROMPT='%F{blue}%~ >%f '; clear; }` Enter
Wait+Screen /code >/
Show

Sleep 600ms
Type "superfind" Enter
Wait+Screen@15s /Sessions/
Sleep 2.2s
Type "refund"
Sleep 2.2s
Ctrl+U
Sleep 300ms
Type "is:waiting"
Sleep 2.4s
Ctrl+U
Sleep 300ms
Type "where did we stop charging people twice"
Sleep 600ms
Ctrl+R
Wait+Screen@30s /Sessions by description/
Sleep 2.5s
Enter
Wait+Screen@10s /Resuming/
Sleep 3.5s
