## Go rate limiter
Package provides an implementation of the leaky token algorithm

- Accepts func through a `Commands` channel and GOrunning it if bandwidth allows
- If refused calls `Cancel()` method, or sends `Command` back if a return channel exists 
- Limits:
    1. n of max concurrent commands – `BurstRate`
    2. n of commands per rate-limit time – `CMDLimitPerTime`
- Custom rate-limit time (per second/minute/..); default: per minute
- Time "after to check" of new tokens issue; default: 1 ms  

NB: don't forget to close a `Commands` channel
