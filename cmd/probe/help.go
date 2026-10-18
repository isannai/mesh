package main

// helpText is what `probe.exe help`, `-h` and `--help` print. Every flag of
// defineFlags has to appear in it (help_test.go): a flag the help never mentions has
// not shipped.
const helpText = `probe: the faucet prober.

It runs as the "probe" mesh app, asks the nodes of its group a question each
slot, and keeps what it asked and what came back. The same program reads that
history back, so no sqlite client is needed.

USAGE
  probe.exe [--config <file>] [-once]    run the prober (isannd starts it this way)
  probe.exe --report <N> [filters] [-a]  print the last N shots
  probe.exe [--id <id>] [--node <text>] [--epoch <n>] [-a]
                                         search the shots; --report is optional
  probe.exe --slots <N>                  draw N image orders and exit
  probe.exe -version                     the version this file was built as
  probe.exe help                         this text

RUN IT IN THE PROBE FOLDER
  The config (conf\probe.json) and the database (db\probe.db) are looked up
  relative to the current folder:

    cd /d D:\isann\artifacts\addon\meshes\probe
    bin\probe.exe --report 20

  From another folder there is no database, and it says so.

OPTIONS
  --config <file>   config file (default: $ISANN_MESH_CONFIG, else conf/probe.json)
  -once             run a single refresh + fire round and exit
  -version          print the version this binary was built as, then exit

  --report <N>      print the last N shots, then the queue and the uptime
  --id <id>         only the shot with this id (the ID column)
  --node <text>     only the shots of nodes whose id contains <text>. A prefix is
                    enough (0xbada8be8), and an id copied from the table works
  --epoch <n>       only the shots fired in this epoch (the EPOCH column)
  -a                under each shot: the question (Q), what was expected, and
                    the answer received (got)

  --slots <N>       draw N image orders (prompt + CLIP checks) and exit

  --id, --node and --epoch can be combined, and with any of them only the
  shots are printed. A value option takes two dashes, a switch takes one.
  A number must be 1 or more and --node needs some text; 0, a negative number,
  an empty --node, or -a without a read option is refused (exit 2) instead of
  starting the prober.

THE TABLE
  ID         the shot's number, for --id
  TIME(UTC)  when it was fired, in UTC whatever this machine's zone is
             (month-day hour:minute:second)
  EPOCH      the slot it was fired in (3 hours, counted in UTC: the slot
             starts at 00, 03, ... 21 UTC)
  NODE       the node asked (cut to 12 characters)
  ROUND      which of the slot's 3 quiz rounds this was; - for a picture
  LATENCY    from submit to the answer in hand
  TOKENS     tokens in the answer
  GOT        the answer received, cut short
  RESULT     pass, fail or truncated when it was judged; otherwise how it ended
             (timeout, refused, queue_full, skipped, submitted)

EXAMPLES
  bin\probe.exe --report 20
  bin\probe.exe --report 20 --node 0xbada8be8
  bin\probe.exe --epoch 165940 --node 0xbada8be8 -a
  bin\probe.exe --id 139 -a
`
