// Copyright 2017 gf Author(https://gitee.com/johng/gf). All Rights Reserved.
//
// This Source Code Form is subject to the terms of the MIT License.
// If a copy of the MIT was not distributed with this file,
// You can obtain one at https://gitee.com/johng/gf.
// Web Server进程间通信 - 子进程

package ghttp

import (
    "os"
    "fmt"
    "time"
    "strings"
    "gitee.com/johng/gf/g/os/gproc"
    "gitee.com/johng/gf/g/os/gtime"
    "gitee.com/johng/gf/g/util/gconv"
    "gitee.com/johng/gf/g/encoding/gjson"
    "gitee.com/johng/gf/g/container/gtype"
    "gitee.com/johng/gf/g/encoding/gbinary"
)

// (子进程)上一次从主进程接收心跳的时间戳
var lastHeartbeatTime = gtype.NewInt()

// 开启所有Web Server(根据消息启动)
func onCommChildStart(pid int, data []byte) {
    if len(data) > 0 {
        sfm := bufferToServerFdMap(data)
        for k, v := range sfm {
            GetServer(k).startServer(v)
        }
    } else {
        serverMapping.RLockFunc(func(m map[string]interface{}) {
            for _, v := range m {
                v.(*Server).startServer(nil)
            }
        })
    }
    heartbeatStarted.Set(true)
}

// 心跳消息
func onCommChildHeartbeat(pid int, data []byte) {
    //glog.Printfln("%d: child heartbeat", gproc.Pid())
    lastHeartbeatTime.Set(int(gtime.Millisecond()))
}

// 子进程收到重启消息，那么将自身的ServerFdMap信息收集后发送给主进程，由主进程进行统一调度
func onCommChildRestart(pid int, data []byte) {
    // 创建新的服务进程，子进程自动从父进程复制文件描述来监听同样的端口
    sfm := getServerFdMap()
    p   := procManager.NewProcess(os.Args[0], os.Args, os.Environ())
    // 将sfm中的fd按照子进程创建时的文件描述符顺序进行整理，以便子进程获取到正确的fd
    for name, m := range sfm {
        for fdk, fdv := range m {
            if len(fdv) > 0 {
                s := ""
                for _, item := range strings.Split(fdv, ",") {
                    array := strings.Split(item, "#")
                    fd    := uintptr(gconv.Uint(array[1]))
                    s     += fmt.Sprintf("%s#%d,", array[0], len(p.GetAttr().Files))
                    p.GetAttr().Files = append(p.GetAttr().Files, os.NewFile(fd, ""))
                }
                sfm[name][fdk] = strings.TrimRight(s, ",")
            }
        }
    }
    p.SetPpid(gproc.Ppid())
    p.Run()
    // 编码，通信
    b, _ := gjson.Encode(sfm)
    sendProcessMsg(p.Pid(),      gMSG_START,    b)
    sendProcessMsg(gproc.Ppid(), gMSG_NEW_FORK, gbinary.EncodeInt(p.Pid()))
    sendProcessMsg(gproc.Pid(),  gMSG_SHUTDOWN, nil)
}

// 友好关闭服务链接并退出
func onCommChildShutdown(pid int, data []byte) {
    serverMapping.RLockFunc(func(m map[string]interface{}) {
        for _, v := range m {
            for _, s := range v.(*Server).servers {
                s.shutdown()
            }
        }
    })
    sendProcessMsg(gproc.Ppid(), gMSG_REMOVE_PROC, gbinary.EncodeInt(gproc.Pid()))
}

// 主进程与子进程相互异步方式发送心跳信息，保持活跃状态
func handleChildProcessHeartbeat() {
    for {
        time.Sleep(gPROC_HEARTBEAT_INTERVAL*time.Millisecond)
        sendProcessMsg(gproc.Ppid(), gMSG_HEARTBEAT, nil)
        // 超过时间没有接收到主进程心跳，自动关闭退出
        if heartbeatStarted.Val() && (int(gtime.Millisecond()) - lastHeartbeatTime.Val() > gPROC_HEARTBEAT_TIMEOUT) {
            sendProcessMsg(gproc.Pid(), gMSG_SHUTDOWN, nil)
            // 子进程有时会无法退出，这里直接使用exit，而不是return
            os.Exit(0)
        }
    }
}