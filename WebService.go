package s

import (
	"encoding/json"
	"fmt"
	"github.com/mitchellh/mapstructure"
	"github.com/ssgo/discover"
	"github.com/ssgo/log"
	"github.com/ssgo/u"
	"net/http"
	"reflect"
	"regexp"
	"strings"
)

type webServiceType struct {
	authLevel     int
	priority      int
	method        string
	path          string
	pathMatcher   *regexp.Regexp
	pathArgs      []string
	parmsNum      int
	inType        reflect.Type
	inIndex       int
	headersIndex  int
	requestIndex  int
	responseIndex int
	loggerIndex   int
	callerIndex   int
	funcType      reflect.Type
	funcValue     reflect.Value
}

var webServices = make(map[string]*webServiceType)
var regexWebServices = make([]*webServiceType, 0)

var inFilters = make([]func(*map[string]interface{}, *http.Request, *http.ResponseWriter) interface{}, 0)
var outFilters = make([]func(*map[string]interface{}, *http.Request, *http.ResponseWriter, interface{}) (interface{}, bool), 0)
var errorHandle func(interface{}, *http.Request, *http.ResponseWriter) interface{}
var webAuthChecker func(int, *string, *map[string]interface{}, *http.Request) bool
var sessionKey string
var clientKey string
var sessionCreator func() string
var sessionObjects = map[*http.Request]map[reflect.Type]interface{}{}
var injectObjects = map[reflect.Type]interface{}{}

// 设置 SessionKey，自动在 Header 中产生，AsyncStart 的客户端支持自动传递
func SetSessionKey(inSessionKey string) {
	if sessionKey == "" {
		sessionKey = inSessionKey
	}
}

func SetClientKey(inClientKey string) {
	if clientKey == "" {
		clientKey = inClientKey
	}
}

// 设置 Session ID 生成器
func SetSessionCreator(creator func() string) {
	sessionCreator = creator
}

// 获取 SessionKey
func GetSessionKey() string {
	return sessionKey
}

// 获取 SessionId
func GetSessionId(request *http.Request) string {
	return request.Header.Get(sessionKey)
}

// 设置一个生命周期在 Request 中的对象，请求中可以使用对象类型注入参数方便调用
func SetSessionInject(request *http.Request, obj interface{}) {
	if sessionObjects[request] == nil {
		sessionObjects[request] = map[reflect.Type]interface{}{}
	}
	sessionObjects[request][reflect.TypeOf(obj)] = obj
}

// 获取本生命周期中指定类型的 Session 对象
func GetSessionInject(request *http.Request, dataType reflect.Type) interface{} {
	if sessionObjects[request] == nil {
		return nil
	}
	return sessionObjects[request][dataType]
}

// 设置一个注入对象，请求中可以使用对象类型注入参数方便调用
func SetInject(obj interface{}) {
	injectObjects[reflect.TypeOf(obj)] = obj
}

// 获取一个注入对象
func GetInject(dataType reflect.Type) interface{} {
	return injectObjects[dataType]
}

// 注册服务
func Register(authLevel int, path string, serviceFunc interface{}) {
	Restful(authLevel, "", path, serviceFunc)
}

// 注册服务
func Restful(authLevel int, method, path string, serviceFunc interface{}) {
	RestfulWithPriority(authLevel, 0, method, path, serviceFunc)
}

// 注册服务
func RegisterWithPriority(authLevel, priority int, path string, serviceFunc interface{}) {
	RestfulWithPriority(authLevel, priority, "", path, serviceFunc)
}

// 注册服务
func RestfulWithPriority(authLevel, priority int, method, path string, serviceFunc interface{}) {
	s, err := makeCachedService(serviceFunc)
	if err != nil {
		logError(err.Error(), "authLevel", authLevel, "priority", priority, "path", path, "method", method)
		return
	}

	s.authLevel = authLevel
	s.priority = priority
	s.method = method
	s.path = path
	finder, err := regexp.Compile("{(.*?)}")
	if err == nil {
		keyName := regexp.QuoteMeta(path)
		finds := finder.FindAllStringSubmatch(path, 20)
		for _, found := range finds {
			keyName = strings.Replace(keyName, regexp.QuoteMeta(found[0]), "(.*?)", 1)
			s.pathArgs = append(s.pathArgs, found[1])
		}
		if len(s.pathArgs) > 0 {
			s.pathMatcher, err = regexp.Compile("^" + keyName + "$")
			if err != nil {
				logError(err.Error(), Map{
					"authLevel": authLevel,
					"priority":  priority,
					"path":      path,
					"method":    method,
				})
				//log.Print("Register	Compile	", err)
			}
			regexWebServices = append(regexWebServices, s)
		}
	}
	if s.pathMatcher == nil {
		webServices[method+path] = s
	}
}

// 设置前置过滤器
func SetInFilter(filter func(in *map[string]interface{}, request *http.Request, response *http.ResponseWriter) (out interface{})) {
	inFilters = append(inFilters, filter)
}

// 设置后置过滤器
func SetOutFilter(filter func(in *map[string]interface{}, request *http.Request, response *http.ResponseWriter, out interface{}) (newOut interface{}, isOver bool)) {
	outFilters = append(outFilters, filter)
}

func SetAuthChecker(authChecker func(authLevel int, url *string, in *map[string]interface{}, request *http.Request) bool) {
	webAuthChecker = authChecker
}

func SetErrorHandle(myErrorHandle func(err interface{}, request *http.Request, response *http.ResponseWriter) interface{}) {
	errorHandle = myErrorHandle
}

func doWebService(service *webServiceType, request *http.Request, response *http.ResponseWriter, args *map[string]interface{},
	result interface{}, requestLogger *log.Logger) (webResult interface{}) {
	// 反射调用
	if result != nil {
		return result
	}
	// 生成参数
	var parms = make([]reflect.Value, service.parmsNum)
	if service.inType != nil {
		if service.inType.Kind() == reflect.Map && service.inType.Elem().Kind() == reflect.Interface {
			parms[service.inIndex] = reflect.ValueOf(args).Elem()
		} else {
			in := reflect.New(service.inType).Interface()
			err := mapstructure.WeakDecode(*args, in)
			if err != nil {
				requestLogger.Error(err.Error())
			}
			parms[service.inIndex] = reflect.ValueOf(in).Elem()
		}
	}
	if service.headersIndex >= 0 {
		parms[service.headersIndex] = reflect.ValueOf(&request.Header)
	}
	if service.requestIndex >= 0 {
		parms[service.requestIndex] = reflect.ValueOf(request)
	}
	if service.responseIndex >= 0 {
		parms[service.responseIndex] = reflect.ValueOf(*response)
	}
	if service.loggerIndex >= 0 {
		parms[service.loggerIndex] = reflect.ValueOf(requestLogger)
	}
	if service.callerIndex >= 0 {
		caller := &discover.Caller{Request: request}
		parms[service.callerIndex] = reflect.ValueOf(caller)
	}
	for i, parm := range parms {
		if parm.Kind() == reflect.Invalid {
			st := service.funcType.In(i)
			isset := false
			if st.Kind() == reflect.Struct || (st.Kind() == reflect.Ptr && st.Elem().Kind() == reflect.Struct) {
				sessObj := GetSessionInject(request, st)
				if sessObj != nil {
					parms[i] = reflect.ValueOf(sessObj)
					isset = true
				} else {
					injectObj := GetInject(st)
					if injectObj != nil {
						injectObjValue := reflect.ValueOf(injectObj)
						setLoggerMethod, found := injectObjValue.Type().MethodByName("SetLogger")
						if found && setLoggerMethod.Type.NumIn() == 2 && setLoggerMethod.Type.In(1).String() == "*log.Logger" {
							setLoggerMethod.Func.Call([]reflect.Value{injectObjValue, reflect.ValueOf(requestLogger)})
						}
						parms[i] = injectObjValue
						isset = true
					}
				}
			}
			if isset == false {
				parms[i] = reflect.New(st).Elem()
			}
		}
	}
	outs := service.funcValue.Call(parms)
	if len(outs) > 0 {
		webResult = outs[0].Interface()
	} else {
		webResult = ""
	}
	return
}

//func makePrintable(data []byte) {
//	n := len(data)
//	for i := 0; i < n; i++ {
//		c := data[i]
//		if c == '\t' || c == '\n' || c == '\r' {
//			data[i] = ' '
//			//} else if c < 32 || c > 126 {
//			//} else if c < 32 {
//			//	data[i] = '?'
//		}
//	}
//}

func makeCachedService(matchedServie interface{}) (*webServiceType, error) {
	// 类型或参数返回值个数不对
	funcType := reflect.TypeOf(matchedServie)
	if funcType.Kind() != reflect.Func {
		return nil, fmt.Errorf("bad Service")
	}

	// 参数类型不对
	targetService := new(webServiceType)
	targetService.parmsNum = funcType.NumIn()
	targetService.inIndex = -1
	targetService.headersIndex = -1
	targetService.requestIndex = -1
	targetService.responseIndex = -1
	targetService.loggerIndex = -1
	targetService.callerIndex = -1
	for i := 0; i < targetService.parmsNum; i++ {
		t := funcType.In(i)
		if t.String() == "*http.Request" {
			targetService.requestIndex = i
		} else if t.String() == "http.ResponseWriter" {
			targetService.responseIndex = i
		} else if t.String() == "*log.Logger" {
			targetService.loggerIndex = i
		} else if t.String() == "*http.Header" {
			targetService.headersIndex = i
		} else if t.String() == "*discover.Caller" {
			targetService.callerIndex = i
		} else if t.Kind() == reflect.Struct || (t.Kind() == reflect.Map && t.Elem().Kind() == reflect.Interface) {
			if targetService.inType == nil {
				targetService.inIndex = i
				targetService.inType = t
			}
		}
	}

	targetService.funcType = funcType
	targetService.funcValue = reflect.ValueOf(matchedServie)
	return targetService, nil
}

func makeBytesResult(data interface{}) []byte {
	bytesResult, err := json.Marshal(data)
	if err != nil || (len(bytesResult) == 4 && string(bytesResult) == "null") {
		t := reflect.TypeOf(data)
		if t.Kind() == reflect.Slice {
			bytesResult = []byte("[]")
		}
		if t.Kind() == reflect.Map {
			bytesResult = []byte("{}")
		}
	}
	u.FixUpperCase(bytesResult, nil)
	return bytesResult
}
