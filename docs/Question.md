# 存在的问题

[] 1、发现Agent客户端会不定时的收到 TUNNEL_REFILL_APPLIED 消息，导致Agent新增了连接，但是实际上当前的tunnel并没有比占用。且新增了连接后，Bridge的idle统计数据只会增长不会下降。
