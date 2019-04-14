# poussetaches

`poussetaches` (which literally means "push tasks" in French) is a lightweight asynchronous task execution service that aims
to replace Celery and RabbitMQ for small Python application.

The app posts base64-encoded payload to `poussetaches` and specify the endpoint that will be used to trigger the task by having 
`poussetaches` making HTTP requests with the registered payload until the right status code is returned.

It works a bit like GCP Cloud Tasks/PubSub in push mode.

It's currently used by [microblog.pub](https://github.com/tsileo/microblog.pub) in production.

## Features

 - Designed as a "sidecar" process (meaning it should have the same lifecycle as the calling app)
 - Lightweight: a single binary with no dependencies
 - Tasks are kept in-memory and dumped to the filesystem to be able to restor from crashes
 - Dead-letter queue for analyzing failed tasks
 - Simple HTTP JSON API for registering tasks and analyzing succeeded, in-progress and dead tasks
 - Scheduled/cron tasks support

## Getting Started

First, you need to setup a secret shared between you app and `poussetaches`, stored as en environment variable: `POUSSETACHES_AUTH_KEY`.
This key will be used by your app to ensure that only `poussetaches` is "executing tasks".

### Creating the handler for receiving tasks (Flask example)

```python
@app.route("task/my_task", methods=["POST"])
def task_my_task():
    # Ensure poussetaches is the author of the request
    if req.headers.get("Poussetaches-Auth-Key") != POUSSETACHES_AUTH_KEY:
        raise ValueError("Bad auth key")

    # Parse the "envelope" which contains metadata and the payload
    envelope = json.loads(req.data)
    print(req)
    print(f"envelope={envelope!r}")
    payload = json.loads(base64.b64decode(envelope["payload"]))

    app.logger.info(envelope["req_id"])
    app.logger.info(envelope["tries"])
    app.logger.info(payload)

    # Return a successfull status code to let pousssetaches knows the task is done
    return ""
```

## API

### GET /

Returns the server status.

### Example

```
$ http get http://localhost:7991/
```

## POST /

Queue a new task.

### Example

```
$ http post http://localhost:7991 url="http://httpbin.org/post" payload=`echo {"payload":"lol"} | base64`
```
