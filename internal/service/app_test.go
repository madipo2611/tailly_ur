package service

import "testing"

func TestSigningFlow(t *testing.T) {
	a := NewApp("", "123456")
	d, e := a.CreateDocument("company", "Act", "act", "+79990000000", "content", "v1", 100)
	if e != nil {
		t.Fatal(e)
	}
	u, e := a.Send(d.ID, "company")
	if e != nil || u == "" {
		t.Fatal(e)
	}
	token := u[len("http://localhost:8080/sign/"):]
	if _, e = a.RequestPEP(token); e != nil {
		t.Fatal(e)
	}
	if _, e = a.ConfirmPEP(token, "+79990000000", "123456", "127.0.0.1", "test", true); e != nil {
		t.Fatal(e)
	}
	if _, e = a.StartUKEP(d.ID, "company", "provider-job"); e != nil {
		t.Fatal(e)
	}
	final, e := a.CompleteUKEP(d.ID, "company", "sig-1")
	if e != nil || final.Status != "completed" {
		t.Fatalf("%v %v", final, e)
	}
}

func TestUploadChangesDocumentHash(t *testing.T) {
	a := NewApp("", "123456")
	d, err := a.CreateDocument("company", "Act", "act", "+79990000000", "body", "v1", 0)
	if err != nil {
		t.Fatal(err)
	}
	uploaded, err := a.Upload(d.ID, "company", []byte("pdf-like bytes"))
	if err != nil || uploaded.ObjectKey == "" || uploaded.ContentHash == hash("body") {
		t.Fatalf("upload = %#v, %v", uploaded, err)
	}
}
func TestDownloadVerifiesUploadedFile(t *testing.T) {
	a := NewApp("", "")
	d, err := a.CreateDocument("company", "Act", "act", "+79990000000", "body", "v1", 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = a.Upload(d.ID, "company", []byte("file")); err != nil {
		t.Fatal(err)
	}
	data, err := a.Download(d.ID, "company")
	if err != nil || string(data) != "file" {
		t.Fatalf("download %q: %v", data, err)
	}
}

func TestLogoutRevokesSession(t *testing.T) {
	a := NewApp("", "123456")
	if _, err := a.RequestLogin("+79990000000"); err != nil {
		t.Fatal(err)
	}
	token, err := a.VerifyLogin("+79990000000", "123456")
	if err != nil || a.User(token) != "+79990000000" {
		t.Fatal("session was not created")
	}
	if err := a.Logout(token); err != nil {
		t.Fatal(err)
	}
	if a.User(token) != "" {
		t.Fatal("session remained authorized")
	}
}
func TestLogoutAllRevokesEverySession(t *testing.T) {
	a := NewApp("", "123456")
	a.RequestLogin("+79990000000")
	first, _ := a.VerifyLogin("+79990000000", "123456")
	a.RequestLogin("+79990000000")
	second, _ := a.VerifyLogin("+79990000000", "123456")
	if err := a.LogoutAll("+79990000000"); err != nil {
		t.Fatal(err)
	}
	if a.User(first) != "" || a.User(second) != "" {
		t.Fatal("active session remained")
	}
}

func TestVerifyAuditDetectsTampering(t *testing.T) {
	a := NewApp("", "123456")
	d, err := a.CreateDocument("company", "Act", "act", "+79990000000", "body", "v1", 0)
	if err != nil {
		t.Fatal(err)
	}
	valid, err := a.VerifyAudit(d.ID, "company")
	if err != nil || !valid {
		t.Fatal("expected valid audit")
	}
	a.audit[0].PayloadHash = "tampered"
	valid, err = a.VerifyAudit(d.ID, "company")
	if err != nil || valid {
		t.Fatal("tampering was not detected")
	}
}
