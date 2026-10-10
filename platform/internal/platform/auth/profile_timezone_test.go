package auth

import (
 "context"
 "testing"

 "github.com/yuqing/platform/internal/pkg/pgtest"
)

// These contract assertions catch the prior unvalidated profile write, without
// requiring a future symbol or a compiler failure to establish RED.
func TestProfileTimezoneMemory(t *testing.T) {
 s, users, _ := newUCService(t)
 seedUCUserOnUserStore(t, users, "timezone-user", "timezone@example.invalid")
 profileTimezoneContract(t,s,users)
}
func TestProfileTimezonePG(t *testing.T) {
 users := NewPGStore(pgtest.Pool(t,"profile_timezone"))
 seedUCUserOnUserStore(t,users,"timezone-user","timezone@example.invalid")
 s:=NewService(users,testSecret,"15m","720h")
 s.EnableUserCenter(users,nil,nil,nil,"")
 profileTimezoneContract(t,s,users)
}
func profileTimezoneContract(t *testing.T,s *Service,users UserStore) {
 ctx:=context.Background()
 for _,zone:=range []string{"Asia/Shanghai","America/New_York","UTC"} {
  if err:=s.UpdateProfile(ctx,"timezone-user","时区用户",zone);err!=nil {t.Fatal(err)}
  p,err:=s.GetProfile(ctx,"timezone-user");if err!=nil||p["timezone"]!=zone {t.Fatalf("saved timezone: %v %v",p,err)}
 }
 for _,zone:=range []string{"Local","not/a_timezone","../../etc/passwd","+08:00"," America/New_York"} {
  t.Run(zone,func(t *testing.T){
   if err:=s.UpdateProfile(ctx,"timezone-user","错误更新",zone);err==nil {t.Errorf("invalid timezone accepted: %q",zone)}
   u,_:=users.GetByID(ctx,"timezone-user");if u.Timezone!="UTC"||u.Name!="时区用户"{t.Error("invalid timezone changed stored profile")}
  })
 }
}
