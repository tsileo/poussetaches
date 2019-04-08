# poussetaches

`poussetaches` (which literraly means "push tasks" in French) is a lightweight asynchronous task execution service that aims
to replace Celery and RabbitMQ for small Python application.

The app posts base64-encoded payload to `poussetaches` and specify the endpoint that will be used to trigger the task.

It works a bit like GCP Cloud Tasks/PubSub in push mode.

It's currently used by [microblog.pub](https://github.com/tsileo/microblog.pub).
