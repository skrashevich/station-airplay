# Embedding a receiver

The root module exports package stationairplay. The command is built from
cmd/station-airplay; go build . no longer produces an executable.

Use one Receiver and one persistent DataDir per physical receiver. New validates
configuration without creating files, requiring aplay or opening sockets.
Missing aplay, unavailable interfaces and occupied ports are Start errors.
Failed startup closes resources opened so far and can be retried.

Start takes the application context. A voice request's context expires when its
response is delivered and must not own background playback. Start is synchronous
and safe to call concurrently. Stop takes the caller's deadline; successful
completion guarantees cleanup. An expired Stop deadline does not permit another
run to overlap: Start returns ErrStopping until cleanup finishes.
Wait observes runtime failure without altering lifetime; Status is safe to poll.

Config.OutputDelay and InitialVolumeDB are optional pointers: nil means 160 ms
and -20 dB; explicit zero values remain valid. Config.Logger accepts a per-instance
standard log.Logger, for example a slog.NewLogLogger adapter.

The library installs no init hooks or signal handlers and never calls os.Exit.
Its HomeKit identity, sessions, player, event hub and PTP selection belong to each
Receiver; there is no singleton receiver. The standard UDP 319/320 PTP ports mean
instances on the same IP cannot run simultaneously. Select configuration by
interface, architecture and ALSA device, without per-model branches.

Status.StateRunning means enabled and discoverable. Connected means a sender owns
the audio session, and Playing reflects its reported rate, not an acoustic
measurement. Stop disconnects the receiver entirely; Control("stop") asks the
sender to stop music while retaining the receiver.

Pause, resume, next and previous require the sender to connect the authenticated
event channel and supply its group UUID. Successful Control means an RTSP 2xx
acknowledgment, not measured playback confirmation. Missing/unsupported commands
return errors; restart has no implemented source opcode. SetVolume and relative
volume commands affect local PCM gain and notify the phone when possible. Phone
slider units map linearly onto -30..0 dB, with zero representing mute. No global
ALSA mixer controls are changed.

Bridge recognizes explicit AirPlay enable/disable/status first. Only a currently
connected sender owns generic media phrases such as «пауза», «продолжи»,
«следующий трек» and «громкость пять». Other requests keep their normal Yandex,
music queue, Home Assistant or LLM route.

For an existing standalone installation, configure Bridge with its state path,
/data/opt/station-airplay/state, disable standalone autostart, start Bridge and
enable AirPlay on demand. Retain the old installation for rollback and do not run
both listeners on the same IP.
