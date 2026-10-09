Output demo/superfind.gif

Set Shell zsh
Set FontSize 15
Set Width 1240
Set Height 640
Set Padding 18
Set Theme "GitHub Dark"
Set TypingSpeed 70ms

Hide
Type `go build -o bin/superfind . && export HOME=/tmp/superfind-demo-home XDG_CACHE_HOME=/tmp/superfind-demo-home/.cache PATH=$PWD/bin:/tmp/superfind-demo-home/bin:$PATH` Enter
Type `python3 demo/make-home.py $HOME && rehash && eval "$(superfind init zsh)" && cd ~/code/storefront && superfind index >/dev/null && superfind label && clear` Enter
Sleep 6s
Show

Type "superfind" Enter
Sleep 2s
Down
Sleep 1.5s
Up
Sleep 1s
Type "refund"
Sleep 2s
Ctrl+U
Sleep 500ms
Type "where did we stop charging people twice"
Sleep 800ms
Ctrl+R
Sleep 4.5s
Tab
Sleep 2.5s
Tab
Sleep 1.5s
Enter
Sleep 2s
