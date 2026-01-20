package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"go/ast"
	"go/parser"
	"go/printer"
	"go/token"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/yaml.v2"
)

type RequestBody struct {
	Database    string `json:"database"`
	Base64Value string `json:"base64value"`
}

// Load config once (highest-impact fix: avoids reading/parsing YAML on every request)
var configMap map[string]interface{}

func main() {
	// Fail fast if config can't be loaded
	if err := loadConfig("config.yml"); err != nil {
		log.Fatalf("Failed to load config.yml: %v", err)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/", indexHandler)
	mux.HandleFunc("/listdbs", ListDatabases)
	mux.HandleFunc("/version", Version)
	mux.HandleFunc("/base64querypostbase64return", Base64QueryPostBase64Return)
	mux.HandleFunc("/base64postjsonreturn", Base64PostJsonReturn)
	mux.HandleFunc("/base64nonquery", Base64NoQuery)

	port := getConfigValue("api_port")
	if port == "" {
		log.Fatal("api_port missing/empty in config.yml")
	}
	addr := ":" + port

	// Highest priority: add server timeouts (won't break functionality)
	server := &http.Server{
		Addr:              addr,
		Handler:           applyCors(mux),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      60 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	certFile := "fullchain.pem"
	keyFile := "localhost.key"

	if _, err := os.Stat(certFile); os.IsNotExist(err) {
		log.Fatalf("Cert file not found: %s", certFile)
	}
	if _, err := os.Stat(keyFile); os.IsNotExist(err) {
		log.Fatalf("Key file not found: %s", keyFile)
	}

	log.Printf("API running on https://localhost%s", addr)

	if err := server.ListenAndServeTLS(certFile, keyFile); err != nil {
		log.Fatalf("Failed to start server: %v", err)
	}
}

// --- Shared helper for the script-backed endpoints ---
// Highest priority fixes here:
// - request body size limit
// - proper returns after http.Error
// - exec timeout + cancellation
// - consistent JSON content-type
func runScriptReturningJSONField(
	w http.ResponseWriter,
	r *http.Request,
	scriptName string,
	responseField string,
) {
	if r.Method != http.MethodPost {
		http.Error(w, "Only POST method is allowed", http.StatusMethodNotAllowed)
		return
	}

	// Avoid unbounded request bodies
	r.Body = http.MaxBytesReader(w, r.Body, 10<<20) // 10MB (adjust as needed)
	defer r.Body.Close()

	var req RequestBody
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	wd, err := os.Getwd()
	if err != nil {
		log.Printf("Getwd error: %v", err)
		http.Error(w, "Server error", http.StatusInternalServerError)
		return
	}

	scriptPath := filepath.Join(wd, scriptName)

	// Use request context + hard timeout so scripts can't hang forever
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, scriptPath, req.Database, req.Base64Value)

	output, err := cmd.CombinedOutput()
	if err != nil {
		// Include script output in logs for debugging (not in response)
		log.Printf("Script failed (%s): %v; output=%s", scriptName, err, string(output))
		http.Error(w, fmt.Sprintf("Error: %v", err), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{
		responseField: string(output),
	})
}

func Base64PostJsonReturn(w http.ResponseWriter, r *http.Request) {
	runScriptReturningJSONField(w, r, "base64_query_json_return.sh", "jsonResultsObj")
}

func Base64QueryPostBase64Return(w http.ResponseWriter, r *http.Request) {
	runScriptReturningJSONField(w, r, "base64_query_base64_return.sh", "base64ResultsObj")
}

func Base64NoQuery(w http.ResponseWriter, r *http.Request) {
	runScriptReturningJSONField(w, r, "base64_non_query.sh", "jsonResultsObj")
}

func Version(w http.ResponseWriter, r *http.Request) {
	// Optional: enforce method if desired; leaving as-is to avoid breaking callers
	cmd := exec.Command("/bin/bash", "./psql_version.sh")

	output, err := cmd.Output()
	if err != nil {
		http.Error(w, fmt.Sprintf("Error running script: %v", err), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(output)
}

func ListDatabases(w http.ResponseWriter, r *http.Request) {
	cmd := exec.Command("/bin/bash", "./psql_listdbs.sh")

	output, err := cmd.Output()
	if err != nil {
		http.Error(w, fmt.Sprintf("Error running script: %v", err), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(output)
}

func getCorsAllowedList() string {
	return getConfigValue("cors_allowed_domains")
}

func applyCors(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		corsString := getCorsAllowedList()

		// Keep existing behavior to avoid breaking functionality:
		// whatever is in config is set as Access-Control-Allow-Origin.
		// (Note: if you store multiple origins, you should implement origin matching.)
		w.Header().Set("Access-Control-Allow-Origin", corsString)
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS, PATCH")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
		// w.Header().Set("Vary", "Origin") // enable if you later implement per-origin matching

		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func indexHandler(w http.ResponseWriter, r *http.Request) {
	apiMethodsList := getApiMethods()
	htmlString := ""
	apiBaseUrl := "https://localhost:3101/"

	htmlString += "<div>"
	htmlString += "<table>"
	htmlString += "  <tbody>"
	htmlString += "    <tr>"
	htmlString += "      <th>"
	htmlString += "        PostgreSql API Methods"
	htmlString += "      </th>"
	htmlString += "    </tr>"

	for _, line := range strings.Split(strings.TrimSuffix(apiMethodsList, "\n"), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		htmlString += "    <tr>"
		htmlString += "      <td>"
		htmlString += "        <a target='_blank' href='" + apiBaseUrl + line + "'>" + line + "</a>"
		htmlString += "      </td>"
		htmlString += "    </tr>"
	}

	htmlString += "     </tbody>"
	htmlString += "   </table>"
	htmlString += "<div>"

	fmt.Fprintln(w, htmlString)
}

// NOTE: This approach is fragile in production (binary won't have main.go).
// I kept it to avoid breaking your current behavior, but improved error handling.
func getApiMethods() string {
	gopath := os.ExpandEnv("$GOPATH")

	// Your original code used: gopath + "./main.go" (often wrong path)
	// Keep close behavior, but at least make it a valid join.
	// If this still doesn't find the file, we just return empty (index page still works).
	fname := filepath.Join(gopath, "main.go")

	srcbuf, err := os.ReadFile(fname)
	if err != nil {
		log.Printf("getApiMethods: unable to read %s: %v", fname, err)
		return ""
	}
	src := string(srcbuf)

	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "main.go", src, 0)
	if err != nil {
		log.Printf("getApiMethods: parse error: %v", err)
		return ""
	}

	var apiMethods string

	ast.Inspect(f, func(n ast.Node) bool {
		fn, ok := n.(*ast.FuncDecl)
		if !ok {
			return true
		}

		if fn.Name != nil && fn.Name.String() == "main" {
			var buf bytes.Buffer
			if err := printer.Fprint(&buf, fset, fn.Body); err != nil {
				log.Printf("getApiMethods: printer error: %v", err)
				return false
			}
			mainFnBody := buf.String()

			for _, line := range strings.Split(strings.TrimSuffix(mainFnBody, "\n"), "\n") {
				if strings.Contains(line, "HandleFunc") && !strings.Contains(line, "indexHandler") {
					res1 := strings.Split(line, "\"")
					if len(res1) > 1 {
						apiMethods += res1[1] + "\n"
					}
				}
			}
			return false
		}

		return true
	})

	return apiMethods
}

// --- config ---
func loadConfig(path string) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var m map[string]interface{}
	if err := yaml.Unmarshal(b, &m); err != nil {
		return err
	}
	configMap = m
	return nil
}

func getConfigValue(configName string) string {
	if configMap == nil {
		return ""
	}
	value, ok := configMap[configName]
	if !ok {
		return ""
	}
	if s, ok := value.(string); ok {
		return s
	}
	return ""
}

// --- AST helpers (unchanged) ---
func fields(fl ast.FieldList) (ret string) {
	pcomma := ""
	for i, f := range fl.List {
		var names string
		ncomma := ""
		for j, n := range f.Names {
			if j > 0 {
				ncomma = ", "
			}
			names = fmt.Sprintf("%s%s%s ", names, ncomma, n)
		}
		if i > 0 {
			pcomma = ", "
		}
		ret = fmt.Sprintf("%s%s%s%s", ret, pcomma, names, expr(f.Type))
	}
	return ret
}

func expr(e ast.Expr) (ret string) {
	switch x := e.(type) {
	case *ast.StarExpr:
		return fmt.Sprintf("%s*%v", ret, x.X)
	case *ast.Ident:
		return fmt.Sprintf("%s%v", ret, x.Name)
	case *ast.ArrayType:
		if x.Len != nil {
			return ""
		}
		res := expr(x.Elt)
		return fmt.Sprintf("%s[]%v", ret, res)
	case *ast.MapType:
		return fmt.Sprintf("map[%s]%s", expr(x.Key), expr(x.Value))
	case *ast.SelectorExpr:
		return fmt.Sprintf("%s.%s", expr(x.X), expr(x.Sel))
	default:
		// Keep original debug behavior minimal
		log.Printf("expr: unhandled AST node: %#v", x)
	}
	return
}
