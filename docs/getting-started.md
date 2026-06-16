# Getting started

This guide gets your StackChan robot talking to a private AI running on your own
computer, with no cloud account. It assumes no prior experience. You will copy a
few commands into a terminal.

It takes about 30 minutes, most of which is the computer downloading things while
you wait.

## What you need

- Your M5Stack StackChan robot and its USB-C cable.
- A computer (macOS or Linux) on the same Wi-Fi as the robot, left on while you use it.
- About 10 GB of free disk space.

## Step 1: Install Docker

Docker is the program that runs warble's pieces for you.

- **macOS / Windows:** download and install [Docker Desktop](https://www.docker.com/products/docker-desktop/),
  then open it. Wait until its whale icon says it's running.
- **Linux:** install [Docker Engine](https://docs.docker.com/engine/install/).

## Step 2 (macOS only): Install Ollama

Ollama runs the AI's "brain." On a Mac it must run outside Docker so it can use
the Apple chip's graphics for speed.

- Install it: `brew install ollama`, or download from [ollama.com](https://ollama.com/download).

On Linux you can skip this; warble runs Ollama for you.

## Step 3: Download warble

Open a terminal (on macOS: the Terminal app) and run:

```bash
git clone https://github.com/rebelthor/warble
cd warble
```

(If you don't have `git`, download the project as a ZIP from the GitHub page,
unzip it, and `cd` into the folder.)

## Step 4: Start warble

```bash
./warble start
```

This one command does everything: it downloads the speech and AI models, builds
the pieces, starts them, and downloads the AI model. The first run takes a while
(several GB of downloads and a one-time build). You'll see progress messages and,
at the end, a green `warble is running.`

If it stops and says Docker isn't running, open Docker Desktop and run
`./warble start` again. It's safe to re-run any time.

## Step 5: Point the robot at your computer

At the end, `./warble start` asks if you want to point the robot at this computer.
Plug the robot in with the USB-C cable and answer yes; it shows what it's about to
do and reprograms the robot to use your computer instead of the cloud. You only do
this once; the robot remembers it.

You can also run it on its own any time:

```bash
./warble connect
```

This needs a small tool called `esptool` on your computer. If `./warble connect`
says it's missing, install it with `pipx install esptool` (or `pip install esptool`)
and run it again.

## Step 6: Talk to it

On the robot's screen, open the **AI Agent** app. Say the wake word or tap the
screen. The light turns to show it's listening, and it answers, all from your
computer.

## How do I know it's working?

```bash
./warble status
```

It lists each part as running or stopped and gives a single ready / not-ready
answer.

## Stopping and starting

```bash
./warble stop      # stop warble
./warble start     # start it again
```

On a Mac, Docker Desktop does not restart on its own after you reboot. After a
restart, open Docker Desktop, then run `./warble start` again.

## If something isn't working

- **`./warble status` says everything is stopped.** Make sure Docker is running
  (on macOS, open Docker Desktop), then run `./warble start`.
- **The robot's listening light never comes on / a message flashes.** The robot
  can't reach your computer. Re-run `./warble start` (it sets the right address),
  then on the robot close and reopen the AI Agent app so it reconnects.
- **It hears you but never answers.** The AI model may still be downloading.
  Run `./warble status`; if it says the model is missing, wait for the download
  or run `./warble start` again.
- **The download is very slow.** That's normal the first time; the AI model is
  several gigabytes. Later runs reuse what's downloaded.

## Going back to the cloud

To return the robot to the M5Stack cloud service (robot plugged in via USB):

```bash
./warble disconnect
```

## Want a different language or voice?

See the "Configuration" section of the [README](../README.md).
