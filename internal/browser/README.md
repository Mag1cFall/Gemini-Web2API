# Chrome Cookie Fetcher 使用文档

批量获取Chrome多配置文件的Google cookies。

## 使用方法

```bash
# 1. 关闭Chrome浏览器
# 2. 运行命令
.\Gemini-Web2API.exe --fetch-cookies

# 3. 按Enter继续

# 4. 选择配置文件
# 输入数字（逗号分隔）：1,2,3
# 或输入 ALL 获取所有配置文件
```

## Cookie保存格式

```env
# 默认账号（无后缀）
__Secure-1PSID=xxx
__Secure-1PSIDTS=xxx

# 其他账号（使用配置文件真实名字）
__Secure-1PSID_niniro=xxx
__Secure-1PSIDTS_niniro=xxx
```

## 功能特性

- **并发处理** - 同时处理多个配置文件，速度快
- **自动重试** - 失败自动重试最多3次
- **顺序保存** - 按照选择顺序保存到.env
- **覆写模式** - 删除所有旧cookies，只保留本次获取的
- **真实名字** - 显示Chrome配置文件的实际名称

## 注意事项

1. **必须关闭Chrome** - 运行前确保Chrome完全关闭
2. **覆写cookies** - 会删除.env里所有旧的`__Secure-1PSID*`，只保留本次获取的
3. **保留其他配置** - ACCOUNTS、PORT等其他配置不受影响
4. **需要登录** - 各配置文件需要登录过gemini.google.com

## 原理

通过Chrome DevTools Protocol (CDP)：
1. 启动Chrome headless模式
2. 导航到gemini.google.com
3. 调用Network.getAllCookies获取所有cookies
4. 提取`__Secure-1PSID`和`__Secure-1PSIDTS`
5. 按顺序保存到.env文件
