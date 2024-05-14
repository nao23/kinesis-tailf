# kinesis-tailf

tail -f command for Amazon Kinesis Data Streams.


## Build

```shell
$ make build
```

## Install

```shell
$ make install
```

## Usage

Required flags are below.

- `-stream`
- `-region` or `AWS_REGION` environment variable

```
Usage of kinesis-tailf:
  -decode-as-msgpack
    	decode record data as msgpack
  -end string
    	end timestamp
  -lf
    	append LF(\n) to each record
  -region string
    	region (default AWS_REGION environment variable)
  -shard-key string
    	shard key
  -start string
    	start timestamp
  -stream string
    	stream name
```

kinesis-tailf supports decoding packed records by Kinesis Producer Library (KPL).

## Licence

MIT
