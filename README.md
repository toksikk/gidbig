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

## To do
- Remove hardcoded sound collections and let the bot build them himself by scanning the `audio/` folder for files.
- Build-in web interface to trigger sounds.  
The idea is to be able to have a detailed listing of available sound files in a responsive interface and a simple button to trigger the sound playback in a channel.

## Known issues
- None that I know of.  

Please use the issue tracker.
