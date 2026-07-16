//go:build darwin

package photos

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestCodeSigningLeafCertificateSupportsNormalDeveloperIDRequirement(t *testing.T) {
	appPath := os.Getenv("TRAWL276_PUBLIC_SIGNED_APP")
	if appPath == "" {
		t.Skip("set TRAWL276_PUBLIC_SIGNED_APP to an installed public Developer ID app")
	}
	requirement, err := exec.Command("/usr/bin/codesign", "--display", "--requirements", "-", appPath).CombinedOutput()
	if err != nil {
		t.Fatalf("read public app designated requirement: %v\n%s", err, requirement)
	}
	if bytes.Contains(requirement, []byte(`certificate leaf = H"`)) {
		t.Fatalf("public app unexpectedly uses the old fixed leaf-hash requirement: %s", requirement)
	}
	certificate, err := codeSigningLeafCertificate(appPath)
	if err != nil {
		t.Fatal(err)
	}
	digest, err := codeSigningLeafDigest(appPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(certificate) == 0 {
		t.Fatal("public app returned an empty leaf certificate")
	}
	t.Logf("boundary=developer_id_leaf_certificate raw_input_app=%q raw_input_requirement=%q raw_output_certificate_bytes=%d raw_output_sha256=%x", appPath, bytes.TrimSpace(requirement), len(certificate), digest)
}

func TestParseFixedLeafRequirementHash(t *testing.T) {
	const digest = "04ac0357bd280de843954d6615292ac9d42dd82b"
	got, err := parseFixedLeafRequirementHash(`designated => identifier "org.opentrawl.synthetic" and certificate leaf = H"` + digest + `"`)
	if err != nil || got != digest {
		t.Fatalf("got %q, error %v", got, err)
	}
	for _, invalid := range []string{
		`designated => identifier "org.opentrawl.synthetic"`,
		`designated => certificate leaf = H"00"`,
		`designated => certificate leaf = H"` + digest + `" and certificate leaf = H"` + digest + `"`,
	} {
		if _, err := parseFixedLeafRequirementHash(invalid); err == nil {
			t.Fatalf("invalid requirement passed: %q", invalid)
		}
	}
}

func TestPhotoKitFetchAppPathUsesOnlyTheCallersLocation(t *testing.T) {
	home := filepath.Join(string(filepath.Separator), "synthetic", "home")
	contents := filepath.Join(string(filepath.Separator), "synthetic", "OpenTrawl.app", "Contents")
	nestedHelper := filepath.Join(contents, "Helpers", "Photoscrawl Fetch.app")
	homeHelper := filepath.Join(home, "Applications", "Photoscrawl Fetch.app")

	tests := []struct {
		name   string
		caller string
		want   string
	}{
		{name: "Mac app", caller: filepath.Join(contents, "MacOS", "Trawl"), want: nestedHelper},
		{name: "bundled trawl", caller: filepath.Join(contents, "Helpers", "trawl"), want: nestedHelper},
		{name: "standalone trawl", caller: filepath.Join(string(filepath.Separator), "synthetic", "bin", "trawl"), want: homeHelper},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := photoKitFetchAppPath(test.caller, home)
			if err != nil {
				t.Fatal(err)
			}
			if got != test.want {
				t.Fatalf("resolved helper = %q, want %q", got, test.want)
			}
			t.Logf("boundary=helper_location raw_input_caller=%q raw_output_helper=%q", test.caller, got)
		})
	}

	for _, caller := range []string{
		filepath.Join(string(filepath.Separator), "synthetic", "Other.app", "Contents", "MacOS", "Trawl"),
		filepath.Join(string(filepath.Separator), "synthetic", "Other.app", "Contents", "Helpers", "trawl"),
		filepath.Join(string(filepath.Separator), "synthetic", "bin", "not-trawl"),
	} {
		if _, err := photoKitFetchAppPath(caller, home); err == nil {
			t.Fatalf("unsupported caller %q resolved a helper", caller)
		}
	}
}

func TestVerifiedPhotoKitFetchAppAcceptsAllThreeCallersWithoutFallback(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	signer := newSyntheticCodeSigner(t, "Synthetic Photos Signing A")
	contents := filepath.Join(root, "OpenTrawl.app", "Contents")
	appCaller := filepath.Join(contents, "MacOS", "Trawl")
	bundledCaller := filepath.Join(contents, "Helpers", "trawl")
	standaloneCaller := filepath.Join(root, "bin", "trawl")
	for _, caller := range []string{appCaller, bundledCaller, standaloneCaller} {
		copySyntheticExecutable(t, caller)
		signer.sign(t, caller, "org.opentrawl.synthetic.caller", "")
	}

	nestedHelper := filepath.Join(contents, "Helpers", "Photoscrawl Fetch.app")
	home := filepath.Join(root, "home")
	homeHelper := filepath.Join(home, "Applications", "Photoscrawl Fetch.app")
	writeSignedSyntheticFetchApp(t, signer, nestedHelper, photoKitFetchBundleID, photoKitFetchExecutable, true)
	writeSignedSyntheticFetchApp(t, signer, homeHelper, photoKitFetchBundleID, photoKitFetchExecutable, true)

	for _, test := range []struct {
		name   string
		caller string
		want   string
	}{
		{name: "Mac app", caller: appCaller, want: nestedHelper},
		{name: "bundled trawl", caller: bundledCaller, want: nestedHelper},
		{name: "standalone trawl", caller: standaloneCaller, want: homeHelper},
	} {
		t.Run(test.name, func(t *testing.T) {
			got, err := verifiedPhotoKitFetchAppPathForCaller(ctx, test.caller, home)
			if err != nil {
				t.Fatal(err)
			}
			if got != test.want {
				t.Fatalf("verified helper = %q, want %q", got, test.want)
			}
		})
	}

	for _, path := range []string{appCaller, bundledCaller, standaloneCaller, nestedHelper, homeHelper} {
		logCodeSignatureEvidence(t, path)
	}

	homeWithOnlyOtherHelper := filepath.Join(root, "home-with-other-helper")
	writeSignedSyntheticFetchApp(t, signer, filepath.Join(homeWithOnlyOtherHelper, "Applications", "Photoscrawl Fetch.app"), photoKitFetchBundleID, photoKitFetchExecutable, true)
	appWithoutNested := filepath.Join(root, "app-without-nested", "OpenTrawl.app", "Contents")
	appWithoutNestedCaller := filepath.Join(appWithoutNested, "MacOS", "Trawl")
	bundledWithoutNestedCaller := filepath.Join(appWithoutNested, "Helpers", "trawl")
	for _, caller := range []string{appWithoutNestedCaller, bundledWithoutNestedCaller} {
		copySyntheticExecutable(t, caller)
		signer.sign(t, caller, "org.opentrawl.synthetic.caller", "")
		if _, err := verifiedPhotoKitFetchAppPathForCaller(ctx, caller, homeWithOnlyOtherHelper); err == nil {
			t.Fatalf("caller %q fell back to the home helper", caller)
		}
	}

	emptyHome := filepath.Join(root, "empty-home")
	if _, err := verifiedPhotoKitFetchAppPathForCaller(ctx, standaloneCaller, emptyHome); err == nil {
		t.Fatal("standalone trawl fell back to the nested helper")
	}
}

func TestVerifiedPhotoKitFetchAppRejectsEveryIdentityMismatch(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	callerSigner := newSyntheticCodeSigner(t, "Synthetic Photos Signing A")
	otherSigner := newSyntheticCodeSigner(t, "Synthetic Photos Signing B")
	caller := filepath.Join(root, "bin", "trawl")
	copySyntheticExecutable(t, caller)
	callerSigner.sign(t, caller, "org.opentrawl.synthetic.caller", "")

	tests := []struct {
		name  string
		build func(string)
	}{
		{name: "missing", build: func(string) {}},
		{name: "unsigned", build: func(path string) {
			writeSyntheticFetchApp(t, path, photoKitFetchBundleID, photoKitFetchExecutable)
		}},
		{name: "differently signed", build: func(path string) {
			writeSignedSyntheticFetchApp(t, otherSigner, path, photoKitFetchBundleID, photoKitFetchExecutable, true)
		}},
		{name: "wrong identifier", build: func(path string) {
			writeSignedSyntheticFetchApp(t, callerSigner, path, "org.opentrawl.synthetic.wrong", photoKitFetchExecutable, true)
		}},
		{name: "wrong entitlement", build: func(path string) {
			writeSignedSyntheticFetchApp(t, callerSigner, path, photoKitFetchBundleID, photoKitFetchExecutable, false)
		}},
		{name: "wrong executable", build: func(path string) {
			writeSignedSyntheticFetchApp(t, callerSigner, path, photoKitFetchBundleID, "wrong-fetch", true)
		}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			appPath := filepath.Join(root, strings.ReplaceAll(test.name, " ", "-"), "Photoscrawl Fetch.app")
			test.build(appPath)
			err := verifyPhotoKitFetchApp(ctx, caller, appPath)
			if err == nil {
				t.Fatal("invalid helper passed verification")
			}
			t.Logf("boundary=helper_rejection raw_input_case=%q raw_output_error=%q", test.name, err)
		})
	}
}

func TestVerifiedPhotoKitFetchAppRejectsTamperingWhenTheSelfSignedCertificateIsUntrusted(t *testing.T) {
	root := t.TempDir()
	signer := newSyntheticCodeSigner(t, "Synthetic Photos Signing A")
	caller := filepath.Join(root, "bin", "trawl")
	helper := filepath.Join(root, "Photoscrawl Fetch.app")
	copySyntheticExecutable(t, caller)
	signer.sign(t, caller, "org.opentrawl.synthetic.caller", "")
	writeSignedSyntheticFetchApp(t, signer, helper, photoKitFetchBundleID, photoKitFetchExecutable, true)
	runSyntheticCommand(t, "/usr/bin/security", "delete-keychain", signer.keychain)

	if err := verifyPhotoKitFetchApp(context.Background(), caller, helper); err != nil {
		t.Fatalf("valid self-signed helper failed without its keychain: %v", err)
	}
	binary := filepath.Join(helper, "Contents", "MacOS", photoKitFetchExecutable)
	file, err := os.OpenFile(binary, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.Write([]byte("tampered")); err != nil {
		_ = file.Close()
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	if err := verifyPhotoKitFetchApp(context.Background(), caller, helper); err == nil {
		t.Fatal("tampered helper passed verification")
	} else {
		t.Logf("boundary=tampered_helper raw_input_mutation=%q raw_output_error=%q", "append tampered bytes after signing", err)
	}
}

func TestBuiltPhotoKitFetchAppMatchesEachSignedCaller(t *testing.T) {
	appPath := os.Getenv("TRAWL276_APP")
	standaloneCaller := os.Getenv("TRAWL276_STANDALONE_TRAWL")
	standaloneHelper := os.Getenv("TRAWL276_STANDALONE_HELPER")
	if appPath == "" || standaloneCaller == "" || standaloneHelper == "" {
		t.Skip("built helper evidence paths are not set")
	}

	nestedHelper := filepath.Join(appPath, "Contents", "Helpers", "Photoscrawl Fetch.app")
	for _, test := range []struct {
		name   string
		caller string
		helper string
	}{
		{name: "Mac app", caller: filepath.Join(appPath, "Contents", "MacOS", "Trawl"), helper: nestedHelper},
		{name: "bundled trawl", caller: filepath.Join(appPath, "Contents", "Helpers", "trawl"), helper: nestedHelper},
		{name: "standalone trawl", caller: standaloneCaller, helper: standaloneHelper},
	} {
		t.Run(test.name, func(t *testing.T) {
			if err := verifyPhotoKitFetchApp(context.Background(), test.caller, test.helper); err != nil {
				t.Fatal(err)
			}
			callerDigest, err := codeSigningLeafDigest(test.caller)
			if err != nil {
				t.Fatal(err)
			}
			helperDigest, err := codeSigningLeafDigest(test.helper)
			if err != nil {
				t.Fatal(err)
			}
			t.Logf("boundary=built_helper_identity raw_input_caller=%q raw_input_helper=%q raw_output_caller_leaf_sha256=%x raw_output_helper_leaf_sha256=%x raw_output=verified", test.caller, test.helper, callerDigest, helperDigest)
		})
	}
	for _, path := range []string{
		filepath.Join(appPath, "Contents", "MacOS", "Trawl"),
		filepath.Join(appPath, "Contents", "Helpers", "trawl"),
		nestedHelper,
		standaloneCaller,
		standaloneHelper,
	} {
		logCodeSignatureEvidence(t, path)
	}
}

type syntheticCodeSigner struct {
	identity string
	keychain string
}

func newSyntheticCodeSigner(t *testing.T, identity string) syntheticCodeSigner {
	t.Helper()
	root := t.TempDir()
	keyPath := filepath.Join(root, "key.pem")
	certPath := filepath.Join(root, "cert.pem")
	p12Path := filepath.Join(root, "identity.p12")
	keychain := filepath.Join(root, strings.ReplaceAll(identity, " ", "-")+".keychain")
	password := "synthetic-test-password"

	runSyntheticCommand(t, "openssl", "req", "-new", "-x509", "-newkey", "rsa:2048", "-nodes", "-days", "2",
		"-keyout", keyPath, "-out", certPath, "-subj", "/CN="+identity,
		"-addext", "keyUsage=critical,digitalSignature",
		"-addext", "extendedKeyUsage=critical,codeSigning",
		"-addext", "basicConstraints=critical,CA:false")
	runSyntheticCommand(t, "openssl", "pkcs12", "-export", "-out", p12Path, "-inkey", keyPath, "-in", certPath,
		"-name", identity, "-passout", "pass:"+password,
		"-certpbe", "PBE-SHA1-3DES", "-keypbe", "PBE-SHA1-3DES", "-macalg", "sha1")
	runSyntheticCommand(t, "/usr/bin/security", "create-keychain", "-p", password, keychain)
	t.Cleanup(func() {
		_ = exec.Command("/usr/bin/security", "delete-keychain", keychain).Run()
	})
	runSyntheticCommand(t, "/usr/bin/security", "unlock-keychain", "-p", password, keychain)
	runSyntheticCommand(t, "/usr/bin/security", "import", p12Path, "-k", keychain, "-P", password, "-T", "/usr/bin/codesign")
	runSyntheticCommand(t, "/usr/bin/security", "set-key-partition-list", "-S", "apple-tool:,apple:,codesign:", "-s", "-k", password, keychain)
	return syntheticCodeSigner{identity: identity, keychain: keychain}
}

func (signer syntheticCodeSigner) sign(t *testing.T, path, identifier, entitlements string) {
	t.Helper()
	args := []string{"--force", "--options", "runtime", "--timestamp=none", "--identifier", identifier}
	if entitlements != "" {
		args = append(args, "--entitlements", entitlements)
	}
	args = append(args, "--keychain", signer.keychain, "--sign", signer.identity, path)
	runSyntheticCommand(t, "/usr/bin/codesign", args...)
}

func writeSignedSyntheticFetchApp(t *testing.T, signer syntheticCodeSigner, appPath, identifier, executable string, photosEntitlement bool) {
	t.Helper()
	writeSyntheticFetchApp(t, appPath, identifier, executable)
	entitlements := filepath.Join(filepath.Dir(appPath), filepath.Base(appPath)+".entitlements.plist")
	value := "false"
	if photosEntitlement {
		value = "true"
	}
	contents := fmt.Sprintf("<?xml version=\"1.0\" encoding=\"UTF-8\"?>\n<plist version=\"1.0\"><dict><key>%s</key><%s/></dict></plist>\n", photoKitPhotosEntitlement, value)
	if err := os.WriteFile(entitlements, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
	signer.sign(t, appPath, identifier, entitlements)
}

func writeSyntheticFetchApp(t *testing.T, appPath, identifier, executable string) {
	t.Helper()
	contents := filepath.Join(appPath, "Contents")
	binary := filepath.Join(contents, "MacOS", executable)
	copySyntheticExecutable(t, binary)
	plist := fmt.Sprintf("<?xml version=\"1.0\" encoding=\"UTF-8\"?>\n<plist version=\"1.0\"><dict><key>CFBundleExecutable</key><string>%s</string><key>CFBundleIdentifier</key><string>%s</string><key>CFBundleName</key><string>Photoscrawl Fetch</string><key>CFBundlePackageType</key><string>APPL</string></dict></plist>\n", executable, identifier)
	if err := os.WriteFile(filepath.Join(contents, "Info.plist"), []byte(plist), 0o600); err != nil {
		t.Fatal(err)
	}
}

func copySyntheticExecutable(t *testing.T, destination string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(destination), 0o700); err != nil {
		t.Fatal(err)
	}
	source, err := os.Open("/bin/echo")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = source.Close() }()
	target, err := os.OpenFile(destination, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o700)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.Copy(target, source); err != nil {
		_ = target.Close()
		t.Fatal(err)
	}
	if err := target.Close(); err != nil {
		t.Fatal(err)
	}
}

func logCodeSignatureEvidence(t *testing.T, path string) {
	t.Helper()
	commands := [][]string{
		{"/usr/bin/codesign", "--display", "--verbose=4", path},
		{"/usr/bin/codesign", "--display", "--requirements", "-", path},
		{"/usr/bin/codesign", "--display", "--entitlements", "-", path},
	}
	for _, command := range commands {
		output, err := exec.Command(command[0], command[1:]...).CombinedOutput()
		if err != nil {
			t.Fatalf("%q: %v\n%s", command, err, output)
		}
		t.Logf("boundary=code_signature raw_input_argv=%q raw_output=\n%s", command, output)
	}
}

func runSyntheticCommand(t *testing.T, name string, args ...string) {
	t.Helper()
	command := exec.Command(name, args...)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("%s %q: %v\n%s", name, args, err, output)
	}
}
