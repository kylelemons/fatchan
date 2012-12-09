Chat Demo
=========

Installing
----------

If you want to install this (as opposed to using `go build` in the directory) you can:

    go install github.com/kylelemons/fatchan/demos/chat

Running
-------

To run the demo, in this directory run:

    go build
    ./chat -addr :1234

Then, in another terminal or four:

    ./chat -server :1234

Then you can chat between the other terminals.
The server only shows messages as they go by, it can't actually talk.
If you close a client either cleanly (^D) or harshly (^C) it should
properly clean up; same with the server, all of the clients should
see the disconnect and exit.
