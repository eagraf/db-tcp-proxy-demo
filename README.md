# TL; DR

A TCP proxy that can route incoming connections with the right application.

Based off of https://github.com/jpillora/go-tcp-proxy

## Longer description


## What this demo does

The docker-compose file describes two database containers, MongoDB and PostgreSQL. In addition, there is a (reverse) proxy container that contains the built Go binary of this demo.

The proxy can take incoming connections from Mongo and Postgres clients, and route them to the right database. A request coming from `psql` will be sent to the Postgres database, a Mongo client will send the request to the Mongo database, even though the URL configured to these clients stayed the same.

## Why would I want this?
Applications need to know the address of the database(s) they rely on to function. In scenarios where this URL could change frequently, it's advantageous for a different service to handle the routing to the right location. This is extra useful when there might be multiple databases in question. A proxy layer also allows for more advanced routing logic, permissions, middleware, and more.

## How does the routing work?
When a new connection is created, the proxy reads in one buffer of input from the client. This buffer is then forwarded to all potential matching databases. The proxy assumes that the correct database will respond within 1 second, and that the connection won't close. If exactly one successful connection is maintained, then all other connections are closed, and the one good connection is maintained, and regular traffic commences. More than one successful connection is considered an error.

## Potential Risks
* This is kind of a hack
* Routing to multiple databases of the same kind might be a bit tricky. Further experimentation is needed
* Databases that have complicated handshakes might be difficult to work with.

## Usage
1. Build the docker container
```
docker build . -t go-tcp-proxy_proxy:latest
```
2. Run docker-compose
```
docker-compose up
```
3. Try using psql to hit the proxy
```
psql -h localhost -p 2143 -U postgres
Password for user postgres: abc
```

4. Watch the proxy route the command to the postgres db
You should be able to use the psql client with the pg db now.

## CLI Usage

```
$ tcp-proxy --help
Usage of tcp-proxy:
  -c: output ansi colors
  -h: output hex
  -l="localhost:9999": local address
  -n: disable nagles algorithm
  -r="localhost:80": remote address
  -match="": match regex (in the form 'regex')
  -replace="": replace regex (in the form 'regex~replacer')
  -v: display server actions
  -vv: display server actions and all tcp data
```

*Note: Regex match and replace*
**only works on text strings**
*and does NOT work across packet boundaries*
