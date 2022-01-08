# Gidbig

Gidbig is a Discord Bot in Go based on
[Airhorn Bot by Hammer and Chisel](https://github.com/hammerandchisel/airhornbot/).  
Visit [https://airhorn.solutions/](https://airhorn.solutions/).

## Features
- Plays Ping Pong with you, if you type "ping" or "pong" in any channel he has access to.
- Airhorn-Feature to play sound files in the voice channel you are currently in.  
Since there is not yet any command to request a list of available sound files, just take a look in `audio/`.  
They are named after the scheme `command_soundname.dca` and would result in a channel command like `!command soundname` like in `!airhorn default`.  
Typing only the `!command` without an argument results in a random sound of this command's collection.
- Comfortable web interface to trigger sounds
- Automatically detect all `.dca` files in `audio/`
- Optional sound descriptions via `.txt` files in `audio/` with the same name as the soundfile

