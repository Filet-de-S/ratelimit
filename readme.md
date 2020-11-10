## Go rate limiter
Package provides an implementation of the leaky token algorithm

- Accepts func through a `Commands` channel and GOrunning it if bandwidth allows; in refused case sends it to a return channel, if exists
- Limits:
    1. n of max parallel commands – `BurstRate`
    2. n of commands per rate-limit time – `CMDLimitPerTime`
- Custom rate-limit time (per second/minute/..); default: per minute
- Time "after to check" of new tokens issue; default: 1 ms  

NB: don't forget to close a `Commands` channel
