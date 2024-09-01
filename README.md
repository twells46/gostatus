# gostatus
This is a simple status bar for X11.
I built it for my personal use with dwm and it performs only the tasks that I need.
This is opposed to something like dwmblocks, which has more flexibility.

If you want to use it, you may need to change internet interface names, battery number, or the sound server depending on your setup.
See the source code.

## Installation
```bash
git clone https://github.com/twells46/gostatus
cd gostatus
go build
sudo cp gostatus /usr/local/bin/
```
This will produce a binary file called `gostatus`, then copy it to your `$PATH`.
