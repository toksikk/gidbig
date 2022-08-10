# Gidbig 🤖

Gidbig is a Discord Bot in Go based on
[Airhorn Bot by Hammer and Chisel](https://github.com/discord/airhornbot/tree/golang).  

## Features

* Plays Ping Pong with you, if you type "ping" or "pong" in any channel he has access to.
* Airhorn-Feature to play sound files in the voice channel you are currently in.  
They are named after the scheme `command_soundname.dca` and would result in a channel command like `!command soundname` like in `!airhorn default`.  
Typing only the `!command` without an argument results in a random sound of this command's collection.
* Comfortable web interface to trigger sounds
* Automatically detect all `.dca` files in `audio/`
* Optional sound descriptions via `.txt` files in `audio/` with the same name as the soundfile
* Plugin functionality
  * [gbp-coffee](https://github.com/toksikk/gbp-coffee/)
  * [gbp-gamerstatus](https://github.com/toksikk/gbp-gamerstatus/)
  * [gbp-wttrin](https://github.com/toksikk/gbp-wttrin/)
  * [gbp-leetoclock](https://github.com/toksikk/gbp-leetoclock/)

## Building Docker image

From the root directory of this repo run the following commands.

```bash
make docker

# run container with mounted config.yaml file
docker run -it --mount type=bind,source=$(pwd)/config.yaml,target=/gidbig/config.yaml gidbig:$(git describe --tags)
```
