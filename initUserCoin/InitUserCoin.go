package main

import (
	"encoding/json"
	"io/ioutil"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/golang/glog"
	"github.com/samuel/go-zookeeper/zk"
)

// UserIDMapResponse 用户id列表接口响应的数据结构
type UserIDMapResponse struct {
	ErrNo  int            `json:"err_no"`
	ErrMsg string         `json:"err_msg"`
	Data   map[string]int `json:"data"`
}

// UserIDMapEmptyResponse 用户id列表接口在用户数为0时候的响应
type UserIDMapEmptyResponse struct {
	ErrNo  int           `json:"err_no"`
	ErrMsg string        `json:"err_msg"`
	Data   []interface{} `json:"data"`
}

// InitUserCoin 拉取用户id列表来初始化用户币种记录
func InitUserCoin(coin string, url string) {
	defer waitGroup.Done()

	// 上次请求接口的时间
	lastPUID := 0

	for {
		// 执行操作
		// 定义在函数中，这样失败时可以简单的return并进入休眠
		func() {
			urlWithLastID := url + "?last_id=" + strconv.Itoa(lastPUID)

			glog.Info("HTTP GET ", urlWithLastID)
			response, err := http.Get(urlWithLastID)

			if err != nil {
				glog.Error("HTTP Request Failed: ", err)
				return
			}

			body, err := ioutil.ReadAll(response.Body)

			if err != nil {
				glog.Error("HTTP Fetch Body Failed: ", err)
				return
			}

			userIDMapResponse := new(UserIDMapResponse)
			err = json.Unmarshal(body, userIDMapResponse)

			if err != nil {
				// 用户id接口在返回0个用户的时候data字段数据类型会由object变成array，需要用另一个struct解析
				userIDMapEmptyResponse := new(UserIDMapEmptyResponse)
				err = json.Unmarshal(body, userIDMapEmptyResponse)

				if err != nil {
					glog.Error("Parse Result Failed: ", err, "; ", string(body))
					return
				}

				glog.Info("Finish: ", coin, "; No New User", "; ", url)
				return
			}

			if userIDMapResponse.ErrNo != 0 {
				glog.Error("API Returned a Error: ", string(body))
				return
			}

			glog.Info("HTTP GET Success. User Num: ", len(userIDMapResponse.Data))

			// 遍历用户币种列表
			for puname, puid := range userIDMapResponse.Data {
				if strings.Contains(puname, "_") {
					// remove coin postfix of puname
					puname = puname[0:strings.LastIndex(puname, "_")]
				}

				err := setMiningCoin(puname, coin)

				if err != nil {
					glog.Info(err.ErrMsg, ": ", puname, ": ", coin)

					if err != APIErrRecordExists {
						continue
					}
				} else {
					glog.Info("success: ", puname, " (", puid, "): ", coin)
				}

				if puid > lastPUID {
					lastPUID = puid
				}
			}

			glog.Info("Finish: ", coin, "; User Num: ", len(userIDMapResponse.Data), "; ", url)
		}()

		// 休眠
		time.Sleep(time.Duration(configData.IntervalSeconds) * time.Second)
	}
}

func setMiningCoin(puname string, coin string) (apiErr *APIError) {

	if len(puname) < 1 {
		apiErr = APIErrPunameIsEmpty
		return
	}

	if strings.Contains(puname, "/") {
		apiErr = APIErrPunameInvalid
		return
	}

	if len(coin) < 1 {
		apiErr = APIErrCoinIsEmpty
		return
	}

	// 检查币种是否存在
	exists := false

	for availableCoin := range configData.UserListAPI {
		if availableCoin == coin {
			exists = true
			break
		}
	}

	if !exists {
		apiErr = APIErrCoinIsInexistent
		return
	}

	if configData.StratumServerCaseInsensitive {
		// stratum server对子账户名大小写不敏感
		// 简单的将子账户名转换为小写即可
		puname = strings.ToLower(puname)
	} else if len(configData.ZKUserCaseInsensitiveIndex) > 0 {
		// stratum server对子账户名大小写敏感
		// 且 ZKUserCaseInsensitiveIndex 未被禁用（不为空）
		// 写入大小写不敏感的用户名索引
		zkIndexPath := configData.ZKUserCaseInsensitiveIndex + strings.ToLower(puname)
		exists, _, err := zookeeperConn.Exists(zkIndexPath)
		if err != nil {
			glog.Error("zk.Exists(", zkIndexPath, ",", puname, ") Failed: ", err)
		}
		if !exists {
			_, err = zookeeperConn.Create(zkIndexPath, []byte(puname), 0, zk.WorldACL(zk.PermAll))
			if err != nil {
				glog.Error("zk.Create(", zkIndexPath, ",", puname, ") Failed: ", err)
			}
		}
	}

	// stratumSwitcher 监控的键
	zkPath := configData.ZKSwitcherWatchDir + puname

	// 看看键是否存在
	exists, _, err := zookeeperConn.Exists(zkPath)

	if err != nil {
		glog.Error("zk.Exists(", zkPath, ") Failed: ", err)
		apiErr = APIErrReadRecordFailed
		return
	}

	if exists {
		// 已经存在，跳过
		apiErr = APIErrRecordExists
		return

	}

	// 不存在，创建
	_, err = zookeeperConn.Create(zkPath, []byte(coin), 0, zk.WorldACL(zk.PermAll))

	if err != nil {
		glog.Error("zk.Create(", zkPath, ",", coin, ") Failed: ", err)
		apiErr = APIErrWriteRecordFailed
		return
	}

	apiErr = nil
	return
}
