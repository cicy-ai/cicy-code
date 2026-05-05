# `dev.py --docker` Fast Refresh

这份 SOP 只讲一件事：`python3 dev.py --docker` 已经跑起来后，如何最快把本地改动更新到 Docker 里的运行实例。

适用范围：

- 本地仓库：`~/projects/cicy-code`
- 当前已经在用 `python3 dev.py --docker`
- 目标是快速验证改动，不是正式发版

## 先搞清楚 `--docker` 实际做了什么

`python3 dev.py --docker` 不会直接把你本地源码目录 mount 进容器里跑 Go。

它的实际流程是：

1. 先在本机执行 `./build.sh docker <tag>`
2. 产出本地 Docker image
3. 再 `docker run ... <runtime_image> --public --agents=...`

这意味着：

- 改 Go 代码后，容器里的 `cicy-code` binary 不会自动更新
- 改 gotty 资产后，容器里的嵌入资源也不会自动更新
- 改 app UI 后，容器镜像里的 UI 也不会自动更新

想让 Docker 里的东西生效，核心思路只有一个：

1. 重新 build
2. 删除旧容器
3. 再跑一次 `python3 dev.py --docker`

## 一眼判断当前是不是 Docker 在跑

```bash
docker ps --format '{{.ID}}\t{{.Image}}\t{{.Status}}\t{{.Ports}}\t{{.Names}}'
```

默认容器名通常是：

```bash
cicy-code-dev
```

看容器日志：

```bash
docker logs -f cicy-code-dev
```

进容器看当前 binary：

```bash
docker exec -it cicy-code-dev sh
which cicy-code
ps -ef | grep cicy-code
```

## 场景 1：只改了 Go 代码，如何最快更新 Docker 里的 Go binary

这是最常见的情况，比如你改了：

- `api/mgr/*.go`
- `api/server/*.go`
- 其他 Go 后端逻辑

最快做法：

```bash
cd ~/projects/cicy-code
./build.sh build linux amd64
docker cp api/cicy-code cicy-code-dev:/app/cicy-code
docker restart cicy-code-dev
```

这套流程的意思是：

1. 本机重新编译 Linux binary
2. 直接把新 binary 覆盖进容器里的 `/app/cicy-code`
3. 重启容器，让 PID 1 的 `cicy-code` 用新 binary 起起来

这是改 Go 代码时最快的刷新方式。

### 注意

先看当前进程：

```bash
docker exec cicy-code-dev ps -ef | grep cicy-code
```

当前 `dev.py --docker` 起出来的容器里，通常是 PID 1 直接跑：

```bash
/app/cicy-code --public --agents=...
```

所以这里最稳妥的刷新方式就是 `docker cp` 新 binary 到 `/app/cicy-code`，然后 `docker restart cicy-code-dev`。

### 什么时候不能只换 binary

下面这些情况，不能只靠 `docker cp` binary：

- 改了 `app/src/*`
- 改了 `api/js/src/*`
- 改了 `api/resources/*`
- 改了 `Dockerfile.runtime`
- 改了运行时依赖

这些都要重建镜像。

### 需要整容器重建时

```bash
cd ~/projects/cicy-code
docker rm -f cicy-code-dev || true
python3 dev.py --docker
```

### 不建议长期混用的做法

原因：

- 容器里的 binary 和 image 状态会不一致
- 容器删掉重建后，这个热替换会消失
- 所以它适合快速验证，不适合作为最终交付状态

## 场景 2：改了 gotty / ttyd assets，如何更新 Docker 里的 gotty assets

这里说的 gotty assets 包括：

- `api/js/src/*`
- `api/resources/*`
- `api/bindata/static/*`

注意，运行时真正用到的是嵌入进 Go binary 的静态资源。  
所以只改文件不够，必须重新做嵌入。

### 最快做法

```bash
cd ~/projects/cicy-code
python3 dev.py --ttyd-assets
./build.sh build linux amd64
docker cp api/cicy-code cicy-code-dev:/app/cicy-code
docker restart cicy-code-dev
```

其中：

- `python3 dev.py --ttyd-assets`
  - 会刷新 gotty/ttyd 前端静态资源
- `./build.sh build linux amd64`
  - 会把新的 gotty assets 重新嵌进 Go binary
- `docker cp`
  - 把新 binary 塞进容器
- 重启容器
  - 让新 binary 生效

### 什么时候要整容器重建

如果你同时还改了下面这些，再走整容器重建：

- `Dockerfile.runtime`
- 运行时依赖
- 容器启动方式

这时用：

```bash
cd ~/projects/cicy-code
docker rm -f cicy-code-dev || true
python3 dev.py --docker
```

## 场景 3：改了 app UI，如何更新 Docker 里的 app UI

这里说的是：

- `app/src/*`
- `app/public/*`

Docker 运行时不是直接读你本地 `app/src`，而是吃 build 后的 UI 产物。

标准流程：

```bash
cd ~/projects/cicy-code
docker rm -f cicy-code-dev || true
python3 dev.py --docker
```

因为 `dev.py --docker` 里会触发：

- `./build.sh docker <tag>`
- 而 `build.sh docker` 默认会先做前端构建

如果你只是想先单独确认 UI build 没问题，可以先跑：

```bash
cd ~/projects/cicy-code/app
npm run build
```

确认没问题后再：

```bash
cd ~/projects/cicy-code
docker rm -f cicy-code-dev || true
python3 dev.py --docker
```

## 场景 4：我同时改了 Go、gotty assets、app UI

不要拆三套流程，直接走一遍完整重建：

```bash
cd ~/projects/cicy-code
docker rm -f cicy-code-dev || true
python3 dev.py --docker
```

这就是最干净的刷新方式。

## 如何确认 Docker 里已经是新版本

### 1. 看容器重新启动时间

```bash
docker ps --format '{{.Names}}\t{{.Status}}'
```

### 2. 看容器日志

```bash
docker logs --tail 200 cicy-code-dev
```

### 3. 如果你改的是 8008 行为，直接测接口

```bash
curl -I http://127.0.0.1:8026/
```

默认 `--docker` 端口一般是：

```bash
8026
```

如果你自己指定了：

```bash
python3 dev.py --docker --port 8030
```

那就测对应端口。

## 最短结论

### 改 Go

```bash
cd ~/projects/cicy-code
./build.sh build linux amd64
docker cp api/cicy-code cicy-code-dev:/app/cicy-code
docker restart cicy-code-dev
```

### 改 gotty assets

```bash
cd ~/projects/cicy-code
python3 dev.py --ttyd-assets
./build.sh build linux amd64
docker cp api/cicy-code cicy-code-dev:/app/cicy-code
docker restart cicy-code-dev
```

### 改 app UI

```bash
cd ~/projects/cicy-code
docker rm -f cicy-code-dev || true
python3 dev.py --docker
```

### 整体重建 Docker

```bash
cd ~/projects/cicy-code
docker rm -f cicy-code-dev || true
python3 dev.py --docker
```
