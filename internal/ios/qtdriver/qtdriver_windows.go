package qtdriver

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"errors"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"
	"unsafe"

	"software.sslmate.com/src/go-pkcs12"
)

// The certificate's private key only exists in the memory of the elevated helper process.

const qtHardwareID = `USB\VID_05AC&PID_12A8&MI_02`

var (
	cfgmgr32              = syscall.NewLazyDLL("cfgmgr32.dll")
	cmLocateDevNode       = cfgmgr32.NewProc("CM_Locate_DevNodeW")
	cmGetDeviceIDListSize = cfgmgr32.NewProc("CM_Get_Device_ID_List_SizeW")
	cmGetDeviceIDList     = cfgmgr32.NewProc("CM_Get_Device_ID_ListW")
	cmGetDevNodeProperty  = cfgmgr32.NewProc("CM_Get_DevNode_Registry_PropertyW")
	wintrust              = syscall.NewLazyDLL("wintrust.dll")
	cdfOpen               = wintrust.NewProc("CryptCATCDFOpen")
	cdfEnumMembers        = wintrust.NewProc("CryptCATCDFEnumMembersByCDFTagEx")
	cdfClose              = wintrust.NewProc("CryptCATCDFClose")
	crypt32               = syscall.NewLazyDLL("crypt32.dll")
	pfxImportCertStore    = crypt32.NewProc("PFXImportCertStore")
	certEnumCertificates  = crypt32.NewProc("CertEnumCertificatesInStore")
	certOpenStore         = crypt32.NewProc("CertOpenStore")
	certAddEncoded        = crypt32.NewProc("CertAddEncodedCertificateToStore")
	certCloseStore        = crypt32.NewProc("CertCloseStore")
	certFreeContext       = crypt32.NewProc("CertFreeCertificateContext")
	mssign32              = syscall.NewLazyDLL("mssign32.dll")
	signerSignEx2         = mssign32.NewProc("SignerSignEx2")
	signerFreeContext     = mssign32.NewProc("SignerFreeSignerContext")
	updateDriverForPnP    = syscall.NewLazyDLL("newdev.dll").NewProc("UpdateDriverForPlugAndPlayDevicesW")
	shellExecuteEx        = syscall.NewLazyDLL("shell32.dll").NewProc("ShellExecuteExW")
)

func utf16Ptr(s string) *uint16 {
	p, _ := syscall.UTF16PtrFromString(s)
	return p
}

// multiSz splits a list of NUL-terminated strings ending with an empty one.
func multiSz(buf []uint16) []string {
	var out []string
	start := 0
	for i, c := range buf {
		if c == 0 {
			if i > start {
				out = append(out, syscall.UTF16ToString(buf[start:i]))
			}
			start = i + 1
		}
	}
	return out
}

// devNodeService is the driver service of a present device instance ("WINUSB" once
// the driver is installed), or "".
func devNodeService(id string) string {
	var node uint32
	if r, _, _ := cmLocateDevNode.Call(uintptr(unsafe.Pointer(&node)), uintptr(unsafe.Pointer(utf16Ptr(id))), 0); r != 0 {
		return ""
	}
	buf := make([]uint16, 256)
	size := uint32(len(buf) * 2)
	const cmDrpService = 0x5
	if r, _, _ := cmGetDevNodeProperty.Call(uintptr(node), cmDrpService, 0, uintptr(unsafe.Pointer(&buf[0])), uintptr(unsafe.Pointer(&size)), 0); r != 0 {
		return ""
	}
	return syscall.UTF16ToString(buf)
}

// Pending reports whether a connected iPhone's capture interface lacks WinUSB.
func Pending() bool {
	filter := utf16Ptr("USB")
	const flags = 0x1 | 0x100 // CM_GETIDLIST_FILTER_ENUMERATOR | CM_GETIDLIST_FILTER_PRESENT
	var size uint32
	if r, _, _ := cmGetDeviceIDListSize.Call(uintptr(unsafe.Pointer(&size)), uintptr(unsafe.Pointer(filter)), flags); r != 0 || size <= 1 {
		return false
	}
	buf := make([]uint16, size)
	if r, _, _ := cmGetDeviceIDList.Call(uintptr(unsafe.Pointer(filter)), uintptr(unsafe.Pointer(&buf[0])), uintptr(size), flags); r != 0 {
		return false
	}
	prefix := strings.ToUpper(qtHardwareID) + `\`
	for _, id := range multiSz(buf) {
		if strings.HasPrefix(strings.ToUpper(id), prefix) && !strings.EqualFold(devNodeService(id), "WINUSB") {
			return true
		}
	}
	return false
}

type shellExecuteInfo struct {
	cbSize        uint32
	mask          uint32
	hwnd          uintptr
	verb          *uint16
	file          *uint16
	parameters    *uint16
	directory     *uint16
	show          int32
	instApp       uintptr
	idList        uintptr
	class         *uint16
	keyClass      uintptr
	hotKey        uint32
	iconOrMonitor uintptr
	process       syscall.Handle
}

const errorCancelled = syscall.Errno(1223) // the user declined the UAC prompt

// runElevated starts this executable with args as administrator (Windows asks the
// user) and waits for its exit code.
func runElevated(c context.Context, args string) (uint32, error) {
	exe, e := os.Executable()
	if e != nil {
		return 0, e
	}
	verb, file, params := utf16Ptr("runas"), utf16Ptr(exe), utf16Ptr(args)
	info := shellExecuteInfo{mask: 0x40 | 0x400, verb: verb, file: file, parameters: params} // SEE_MASK_NOCLOSEPROCESS | SEE_MASK_FLAG_NO_UI
	info.cbSize = uint32(unsafe.Sizeof(info))
	r, _, e := shellExecuteEx.Call(uintptr(unsafe.Pointer(&info)))
	runtime.KeepAlive(verb)
	runtime.KeepAlive(file)
	runtime.KeepAlive(params)
	if r == 0 {
		return 0, e
	}
	defer syscall.CloseHandle(info.process)
	for {
		if event, _ := syscall.WaitForSingleObject(info.process, 200); event == 0 {
			break
		}
		if c.Err() != nil {
			return 0, c.Err()
		}
	}
	var code uint32
	if e = syscall.GetExitCodeProcess(info.process, &code); e != nil {
		return 0, e
	}
	return code, nil
}

// Install gives the connected iPhones' QuickTime capture interface the WinUSB driver.
// It runs this executable again elevated (--internal-install-qt-driver), so Windows
// asks for administrator rights; call it only when Pending.
func Install(c context.Context) error {
	token := make([]byte, 8)
	rand.Read(token)
	result := filepath.Join(os.TempDir(), "deviceshell-qt-driver-"+hex.EncodeToString(token)+".txt")
	defer os.Remove(result)
	code, e := runElevated(c, `--internal-install-qt-driver "`+result+`"`)
	switch {
	case errors.Is(e, errorCancelled):
		return errors.New("USB video driver not installed: administrator permission was declined")
	case e != nil:
		return fmt.Errorf("USB video driver not installed: %w", e)
	case code != 0:
		message, _ := os.ReadFile(result)
		return fmt.Errorf("USB video driver not installed: %s", strings.TrimSpace(string(message)))
	}
	return nil
}

// RunInstall is the administrator side of Install
// (deviceshell.exe --internal-install-qt-driver <result file>); the parent reads the
// error, if any, from the result file.
func RunInstall(result string) int {
	e := installQTDriver()
	if e != nil {
		os.WriteFile(result, []byte(e.Error()), 0600)
		return 1
	}
	return 0
}

func installQTDriver() error {
	if !Pending() {
		return nil
	}
	var e error
	stage, e := os.MkdirTemp("", "deviceshell-qt-driver-")
	if e != nil {
		return e
	}
	defer os.RemoveAll(stage)
	infPath := filepath.Join(stage, "qt-driver.inf")
	if e = os.WriteFile(infPath, driverINF, 0600); e != nil {
		return e
	}
	if e = qtCatalog(stage); e != nil {
		return fmt.Errorf("catalog: %w", e)
	}
	if e = qtSignTrusted(filepath.Join(stage, "qt-driver.cat")); e != nil {
		return fmt.Errorf("firma: %w", e)
	}
	var reboot int32
	// INSTALLFLAG_FORCE: Apple's Ethernet driver outranks a locally signed one.
	r, _, e := updateDriverForPnP.Call(0, uintptr(unsafe.Pointer(utf16Ptr(qtHardwareID))), uintptr(unsafe.Pointer(utf16Ptr(infPath))), 1, uintptr(unsafe.Pointer(&reboot)))
	if r == 0 {
		return fmt.Errorf("UpdateDriverForPlugAndPlayDevices: %w", e)
	}
	return nil
}

// qtCatalog writes qt-driver.cat (SHA-256, catalog version 2) for qt-driver.inf in
// dir, through the catalog definition API that makecat uses.
func qtCatalog(dir string) error {
	const cdf = "[CatalogHeader]\r\nName=qt-driver.cat\r\nResultDir=.\r\nPublicVersion=0x0000001\r\nCatalogVersion=2\r\nHashAlgorithms=SHA256\r\nEncodingType=0x00010001\r\n[CatalogFiles]\r\n<hash>qt-driver.inf=qt-driver.inf\r\n"
	if e := os.WriteFile(filepath.Join(dir, "qt-driver.cdf"), []byte(cdf), 0600); e != nil {
		return e
	}
	// The definition names files relative to the working directory; this helper
	// process does nothing else.
	previous, _ := os.Getwd()
	if e := os.Chdir(dir); e != nil {
		return e
	}
	defer os.Chdir(previous)
	var parseErr error
	callback := syscall.NewCallback(func(area, code uintptr, line *uint16) uintptr {
		parseErr = fmt.Errorf("catalog definition: area %d, error %#x", area, code)
		return 0
	})
	handle, _, e := cdfOpen.Call(uintptr(unsafe.Pointer(utf16Ptr("qt-driver.cdf"))), callback)
	if handle == 0 {
		return fmt.Errorf("CryptCATCDFOpen: %w", e)
	}
	var member, tag uintptr
	for {
		tag, _, _ = cdfEnumMembers.Call(handle, tag, callback, uintptr(unsafe.Pointer(&member)), 0, 0)
		if tag == 0 {
			break
		}
	}
	if r, _, e := cdfClose.Call(handle); r == 0 {
		return fmt.Errorf("CryptCATCDFClose: %w", e)
	}
	if parseErr != nil {
		return parseErr
	}
	if _, e := os.Stat("qt-driver.cat"); e != nil {
		return e
	}
	return nil
}

// addMachineCertificate puts a certificate in a LocalMachine store (needs administrator).
func addMachineCertificate(store string, der []byte) error {
	const certStoreProvSystemW, certSystemStoreLocalMachine = 10, 0x20000
	handle, _, e := certOpenStore.Call(certStoreProvSystemW, 0, 0, certSystemStoreLocalMachine, uintptr(unsafe.Pointer(utf16Ptr(store))))
	if handle == 0 {
		return fmt.Errorf("store %s: %w", store, e)
	}
	defer certCloseStore.Call(handle, 0)
	const encoding, replaceExisting = 0x10001, 3 // X509_ASN_ENCODING | PKCS_7_ASN_ENCODING
	if r, _, e := certAddEncoded.Call(handle, encoding, uintptr(unsafe.Pointer(&der[0])), uintptr(len(der)), replaceExisting, 0); r == 0 {
		return fmt.Errorf("store %s: %w", store, e)
	}
	return nil
}

// qtSignTrusted creates the throwaway code-signing certificate, makes this PC trust
// it and signs the catalog with it.
func qtSignTrusted(catalog string) error {
	key, cert, der, e := qtCertificate()
	if e != nil {
		return e
	}
	for _, store := range []string{"Root", "TrustedPublisher"} {
		if e = addMachineCertificate(store, der); e != nil {
			return e
		}
	}
	return signCatalog(catalog, key, cert)
}

func qtCertificate() (*rsa.PrivateKey, *x509.Certificate, []byte, error) {
	key, e := rsa.GenerateKey(rand.Reader, 2048)
	if e != nil {
		return nil, nil, nil, e
	}
	serial, _ := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 64))
	now := time.Now()
	template := &x509.Certificate{
		SerialNumber: serial, Subject: pkix.Name{CommonName: "DeviceShell iPhone QuickTime driver"},
		NotBefore: now.Add(-time.Hour), NotAfter: now.AddDate(20, 0, 0),
		KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageCodeSigning},
		BasicConstraintsValid: true,
	}
	der, e := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if e != nil {
		return nil, nil, nil, e
	}
	cert, e := x509.ParseCertificate(der)
	return key, cert, der, e
}

type cryptDataBlob struct {
	size uint32
	data *byte
}

type signerFileInfo struct {
	cbSize uint32
	name   *uint16
	file   uintptr
}

type signerSubjectInfo struct {
	cbSize uint32
	index  *uint32
	choice uint32
	file   *signerFileInfo
}

type signerCertStoreInfo struct {
	cbSize uint32
	cert   uintptr
	policy uint32
	store  uintptr
}

type signerCert struct {
	cbSize uint32
	choice uint32
	store  *signerCertStoreInfo
	hwnd   uintptr
}

type signerSignatureInfo struct {
	cbSize          uint32
	hashAlgorithm   uint32
	attrChoice      uint32
	authcode        uintptr
	authenticated   uintptr
	unauthenticated uintptr
}

// signCatalog adds an Authenticode SHA-256 signature to the catalog. The key goes to
// Windows as an in-memory PKCS#12 import that is never persisted.
func signCatalog(catalog string, key *rsa.PrivateKey, cert *x509.Certificate) error {
	password := make([]byte, 16)
	rand.Read(password)
	pass := hex.EncodeToString(password)
	pfx, e := pkcs12.LegacyDES.Encode(key, cert, nil, pass)
	if e != nil {
		return e
	}
	blob := cryptDataBlob{uint32(len(pfx)), &pfx[0]}
	// The signer needs the key in a CNG provider (a legacy CSP gives NTE_BAD_TYPE).
	const pkcs12NoPersistKey, pkcs12PreferCNGKSP = 0x8000, 0x100
	store, _, e := pfxImportCertStore.Call(uintptr(unsafe.Pointer(&blob)), uintptr(unsafe.Pointer(utf16Ptr(pass))), pkcs12NoPersistKey|pkcs12PreferCNGKSP)
	runtime.KeepAlive(pfx)
	if store == 0 {
		return fmt.Errorf("PFXImportCertStore: %w", e)
	}
	defer certCloseStore.Call(store, 0)
	context, _, _ := certEnumCertificates.Call(store, 0)
	if context == 0 {
		return errors.New("certificate not imported")
	}
	defer certFreeContext.Call(context)
	var index uint32
	name := utf16Ptr(catalog)
	file := signerFileInfo{name: name}
	file.cbSize = uint32(unsafe.Sizeof(file))
	subject := signerSubjectInfo{index: &index, choice: 1, file: &file} // SIGNER_SUBJECT_FILE
	subject.cbSize = uint32(unsafe.Sizeof(subject))
	storeInfo := signerCertStoreInfo{cert: context, policy: 1, store: store} // SIGNER_CERT_POLICY_STORE
	storeInfo.cbSize = uint32(unsafe.Sizeof(storeInfo))
	signer := signerCert{choice: 2, store: &storeInfo} // SIGNER_CERT_STORE
	signer.cbSize = uint32(unsafe.Sizeof(signer))
	signature := signerSignatureInfo{hashAlgorithm: 0x800c} // CALG_SHA_256, SIGNER_NO_ATTR
	signature.cbSize = uint32(unsafe.Sizeof(signature))
	var signerContext uintptr
	hr, _, _ := signerSignEx2.Call(0, uintptr(unsafe.Pointer(&subject)), uintptr(unsafe.Pointer(&signer)), uintptr(unsafe.Pointer(&signature)),
		0, 0, 0, 0, 0, 0, uintptr(unsafe.Pointer(&signerContext)), 0, 0)
	runtime.KeepAlive(name)
	if signerContext != 0 {
		signerFreeContext.Call(signerContext)
	}
	if hr != 0 {
		return fmt.Errorf("SignerSignEx2: HRESULT %#x", uint32(hr))
	}
	return nil
}
