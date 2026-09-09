# 基础工具实施计划

目标：用标准库实现命令执行、后台进程、文件操作、文本工具和条件等待。

用户已授权自主划分模块、实现后直接提交推送。在当前干净工作区完成，避免引入额外配置和依赖。

设计约定：
- executil：Run/RunEnv 返回合并输出和 error；Start/StartEnv 接受分块输出回调，返回 Process；提供 PID、Running、Wait、Stop。继承环境并覆盖额外变量，使用 bash -c。Unix 停止整个进程组。
- fileutil：Files/Dirs 仅列直接子项，返回排序后的路径；Read/Write 使用字符串；Latest 按修改时间，空目录 panic；Grep 复用 strutil.Lines，按大小写敏感的字面子串 OR 匹配，once 只返回首行。所有 I/O 失败 panic。
- strutil：Random 使用 ASCII 字母数字，零长度返回空串，负长度 panic；Lines 支持 LF/CRLF/CR，保留内部空行，不产生结尾换行对应的额外空行，空输入返回空切片。
- waiter：Wait(timeout, interval time.Duration, condition func() bool, message string) error；立即检查，然后按间隔重试；同步执行条件，不泄漏 goroutine；超时错误支持 errors.Is(context.DeadlineExceeded)。不能中断阻塞的条件函数，超时后返回的 true 不算成功。

执行与验证：
- [x] 为四个模块建立行为测试，验证尚未实现时失败。
- [x] 实现 strutil/fileutil，覆盖行分隔、长行、多关键字、目录筛选、最新文件和 panic。
- [x] 实现 waiter，覆盖立即成功、重试、超时、参数和慢条件。
- [x] 实现 executil，覆盖 shell 语法、环境继承覆盖、失败输出、实时输出、等待和停止、并发状态。
- [x] 提供全部公开接口文档、示例和默认行为，更新唯一模块索引。
- [x] 独立代码审查，执行 fmt/test/vet/build/race，检查文档链接。
交付步骤：提交并推送当前分支，确认远端提交。
