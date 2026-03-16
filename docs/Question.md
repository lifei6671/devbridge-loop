# 存在的问题

[] 1、我发现，启动Agent和Bridge之后，使用grpc作为tunnel的承载，Agent显式的tunnel会持续重建，且日志有大量报错：tunnel pool event trigger=pool_rebuilt idle=8 active=0 total=8 reason=wait traffic open failed: wait traffic open: read first frame: read payload: read tunnel: EOF