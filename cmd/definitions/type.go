//go:build tools
// +build tools

package main

import (
	"fmt"
	"os"
	"sort"

	"github.com/Xuanwo/templateutils"
	"github.com/pelletier/go-toml"
	log "github.com/sirupsen/logrus"

	"go.beyondstorage.io/v5/definitions"
)

// Data is the biggest container for all definitions.
type Data struct {
	FeaturesMap map[string]*Feature
	PairsMap    map[string]*Pair
	// scope -> category -> name -> info
	InfosMap      map[string]map[string]map[string]*Info
	FieldsMap     map[string]*Field
	OperationsMap map[string]map[string]*Operation
	InterfacesMap map[string]*Interface

	Service *Service
}

// NewData will formatGlobal the whole data.
func NewData() *Data {
	data := &Data{}

	data.LoadFeatures()
	data.LoadPairs()
	data.LoadInfos()
	data.LoadFields()
	data.LoadOperations()
	return data
}

func (d *Data) Interfaces() []*Interface {
	var ins []*Interface
	for _, v := range d.InterfacesMap {
		v := v
		ins = append(ins, v)
	}
	sort.Slice(ins, func(i, j int) bool {
		return ins[i].Name < ins[j].Name
	})
	return ins
}

func (d *Data) StorageMeta() []*Info {
	var infos []*Info
	for _, v := range d.InfosMap["storage"]["meta"] {
		v := v
		infos = append(infos, v)
	}

	sort.Slice(infos, func(i, j int) bool {
		return compareInfo(infos[i], infos[j])
	})
	return infos
}

func (d *Data) ObjectMeta() []*Info {
	var infos []*Info
	for _, v := range d.InfosMap["object"]["meta"] {
		v := v
		infos = append(infos, v)
	}

	sort.Slice(infos, func(i, j int) bool {
		return compareInfo(infos[i], infos[j])
	})
	return infos
}

func (d *Data) Features() []*Feature {
	var feats []*Feature
	for _, v := range d.FeaturesMap {
		v := v
		feats = append(feats, v)
	}
	sort.Slice(feats, func(i, j int) bool {
		return feats[i].Name < feats[j].Name
	})
	return feats
}

func (d *Data) LoadFeatures() {
	err := parseTOML(loadBindata(featurePath), &d.FeaturesMap)
	if err != nil {
		log.Fatalf("parse feature: %v", err)
	}

	defaultNs := []string{"service", "storage"}
	for k, v := range d.FeaturesMap {
		v.Name = k
		for _, ns := range v.Namespaces {
			if ns != "service" && ns != "storage" {
				log.Fatalf("invalid namespace %s for feature %s", ns, k)
			}
		}
		if len(v.Namespaces) == 0 {
			v.Namespaces = append(v.Namespaces, defaultNs...)
		}
	}
}

// LoadPairs will formatGlobal PairsMap for pair spec
func (d *Data) LoadPairs() {
	err := parseTOML(loadBindata(pairPath), &d.PairsMap)
	if err != nil {
		log.Fatalf("parse pair: %v", err)
	}

	// Inject pairs
	d.PairsMap["context"] = &Pair{
		Type: "context.Context",
	}
	d.PairsMap["http_client_options"] = &Pair{
		Type: "*httpclient.Options",
	}

	var defaultPairs []*Pair
	for k, v := range d.PairsMap {
		v.Name = k
		// Pairs from bindata must be global.
		v.Global = true

		if v.Defaultable {
			defaultPairs = append(defaultPairs, &Pair{
				Name:        fmt.Sprintf("default_%s", v.Name),
				Type:        v.Type,
				Description: v.Description,
				Global:      true,
			})
		}
	}
	for _, v := range defaultPairs {
		v := v
		d.PairsMap[v.Name] = v
	}
}

func (d *Data) LoadInfos() {
	d.InfosMap = map[string]map[string]map[string]*Info{
		"object": {
			"meta": nil,
		},
		"storage": {
			"meta": nil,
		},
	}

	omm := make(map[string]*Info)
	err := parseTOML(loadBindata(infoObjectMeta), &omm)
	if err != nil {
		log.Fatalf("parse pair: %v", err)
	}
	d.InfosMap["object"]["meta"] = omm

	smm := make(map[string]*Info)
	err = parseTOML(loadBindata(infoStorageMeta), &smm)
	if err != nil {
		log.Fatalf("parse pair: %v", err)
	}
	d.InfosMap["storage"]["meta"] = smm

	for scope, v := range d.InfosMap {
		for category, v := range v {
			for name, v := range v {
				v.Name = name
				v.Category = category
				v.Scope = scope
				// SortedInfos from bindata must be global.
				v.Global = true
			}
		}
	}
}

func (d *Data) LoadFields() {
	err := parseTOML(loadBindata(fieldPath), &d.FieldsMap)
	if err != nil {
		log.Fatalf("parse field: %v", err)
	}
	for k, v := range d.FieldsMap {
		v.Name = k
	}
}

func (d *Data) LoadOperations() {
	err := parseTOML(loadBindata(operationPath), &d.InterfacesMap)
	if err != nil {
		log.Fatalf("parse operations: %v", err)
	}

	d.OperationsMap = map[string]map[string]*Operation{
		"service": make(map[string]*Operation),
		"storage": make(map[string]*Operation),
	}
	for k, v := range d.InterfacesMap {
		v.Name = k

		for name, op := range v.Op {
			op.Name = name
			op.d = d

			if op.Name != "features" {
				op.Params = append(op.Params, "pairs")
			}
			if !op.Local {
				op.Results = append(op.Results, "err")
			}

			op := op
			if k == "servicer" {
				d.OperationsMap["service"][op.Name] = op
			} else if k == "storager" {
				// All operations for storage have been added back to storager.
				d.OperationsMap["storage"][op.Name] = op
			}
		}
	}
}

// ValidateNamespace will inject a namespace to insert generated PairsMap.
func (d *Data) ValidateNamespace(n *Namespace) {
	for _, v := range n.ParsedFunctions() {
		// For now, we disallow required Pairs for Storage.
		if n.Name == "Storage" && len(v.Required) > 0 {
			log.Fatalf("Operation [%s] cannot specify required Pairs.", v.Name)
		}

		existPairs := map[string]bool{}
		log.Infof("check function %s", v.Name)
		for _, p := range v.Optional {
			existPairs[p] = true
		}

		op := v.GetOperation()
		for _, ps := range op.Pairs {
			if existPairs[ps] {
				continue
			}
			log.Fatalf("Operation [%s] requires Pair [%s] support, please add virtual implementation for this pair.", v.Name, ps)
		}
	}
}

func (d *Data) LoadService(filePath string) {
	bs, err := os.ReadFile(filePath)
	if err != nil {
		log.Fatalf("read file %s: %v", filePath, err)
	}

	d.Service = &Service{
		d: d,
	}
	err = parseTOML(bs, d.Service)
	if err != nil {
		log.Fatalf("parse service: %v", err)
	}

	srv := d.Service

	// Handle pairs
	if srv.Pairs == nil {
		srv.Pairs = make(map[string]*Pair)
	}
	var defaultPairs []*Pair
	for k, v := range srv.Pairs {
		v.Name = k

		if v.Defaultable {
			defaultPairs = append(defaultPairs, &Pair{
				Name:        fmt.Sprintf("default_%s", v.Name),
				Type:        v.Type,
				Description: v.Description,
			})
		}
	}
	for _, v := range defaultPairs {
		v := v
		srv.Pairs[v.Name] = v
	}

	// Handle all infos
	for scope, v := range srv.Infos {
		for category, v := range v {
			for name, v := range v {
				v.Name = name
				v.Category = category
				v.Scope = scope
			}
		}
	}

	// Handle namespace.
	for name, ns := range srv.Namespaces {
		ns.Name = name
		ns.srv = srv

		// When no function is declared under the namespace of the service, we should initialize the map `ns.Op`.
		if ns.Op == nil {
			ns.Op = make(map[string]*Function, 0)
		}

		interfaceName := fmt.Sprintf("%sr", ns.Name)
		in := d.InterfacesMap[interfaceName]

		// Handle features.
		for _, featureName := range ns.Features {
			f, ok := d.FeaturesMap[featureName]
			if ok {
				// check namespace
				ok = false
				for _, fns := range f.Namespaces {
					if fns == ns.Name {
						ok = true
						break
					}
				}
				if !ok {
					log.Fatalf("%s unsupported in %s", featureName, ns.Name)
				}
			} else {
				for _, op := range in.Op {
					if op.Name == featureName {
						ok = true
						break
					}
				}
				if !ok {
					log.Fatalf("feature not registered: %s", featureName)
				}
			}

			if featureName == "loose_pair" {
				ns.HasFeatureLoosePair = true
			}

			// Generate enable virtual feature pairs.
			if f != nil && f.Virtual {
				pairName := fmt.Sprintf("enable_%s", featureName)
				srv.Pairs[pairName] = &Pair{
					Name:        pairName,
					Type:        "bool",
					Description: f.Description,
				}
			}
		}

		// Handle New function.
		if ns.New == nil {
			ns.New = &Function{}
		}
		ns.New.Name = "new"
		ns.New.srv = srv
		ns.New.ns = ns
		ns.New.Implemented = true
		for _, v := range ns.ParsedDefaultable() {
			ns.New.Optional = append(ns.New.Optional, "default_"+v.Pair.Name)
		}
		// add default_*_pairs and *_virtual_features
		hasDefaultNsPairs := false
		hasNsFeatures := false
		defaultNsPairsName := fmt.Sprintf("default_%s_pairs", ns.Name)
		// Deprecated: Renamed to *_virtual_features
		nsFeaturesName := fmt.Sprintf("%s_features", ns.Name)
		nsVirtualFeaturesName := fmt.Sprintf("%s_virtual_features", ns.Name)
		for _, v := range ns.New.Optional {
			if v == defaultNsPairsName {
				log.Warnf("Please remove %s pair as it will be automatically generated.", defaultNsPairsName)
				hasDefaultNsPairs = true
			}
			if v == nsFeaturesName || v == nsVirtualFeaturesName {
				log.Warnf("Please remove %s pair or %s pair as it will be automatically generated.", nsFeaturesName, nsVirtualFeaturesName)
				hasNsFeatures = true
			}
		}
		if !hasDefaultNsPairs {
			ns.New.Optional = append(ns.New.Optional, defaultNsPairsName)
			srv.Pairs[defaultNsPairsName] = &Pair{
				Name: defaultNsPairsName,
				Type: templateutils.ToPascal(defaultNsPairsName),
			}
		}
		if !hasNsFeatures {
			ns.New.Optional = append(ns.New.Optional, nsFeaturesName)
			srv.Pairs[nsFeaturesName] = &Pair{
				Name:        nsFeaturesName,
				Type:        templateutils.ToPascal(nsFeaturesName),
				Description: fmt.Sprintf("Deprecated: Use %s instead.", nsVirtualFeaturesName),
			}

			ns.New.Optional = append(ns.New.Optional, nsVirtualFeaturesName)
			srv.Pairs[nsVirtualFeaturesName] = &Pair{
				Name: nsVirtualFeaturesName,
				Type: templateutils.ToPascal(nsVirtualFeaturesName),
			}
		}

		// Handle other functions.
		for k, v := range ns.Op {
			v.srv = srv
			v.ns = ns
			v.Name = k
			v.Implemented = true
		}

		// Service could not declare all ops, so we need to fill them instead.
		for _, op := range in.Op {
			if _, ok := ns.Op[op.Name]; ok {
				continue
			}
			ns.Op[op.Name] = &Function{
				srv:         srv,
				ns:          ns,
				Name:        op.Name,
				Implemented: false,
			}
		}

		implemented := parseFunc(ns.Name)
		for k := range implemented {
			sk := templateutils.ToSnack(k)
			if _, ok := ns.Op[sk]; !ok {
				continue
			}
			ns.Op[sk].Implemented = true
		}

		//d.ValidateNamespace(ns)
	}
}

// Service is the service definition.
type Service struct {
	d *Data

	Name       string                `toml:"name"`
	Namespaces map[string]*Namespace `toml:"namespace"`
	Pairs      map[string]*Pair      `toml:"pairs"`
	// scope -> category -> name -> info
	Infos map[string]map[string]map[string]*Info `toml:"infos"`
}

func (s *Service) SortedNamespaces() []*Namespace {
	var ns []*Namespace
	for _, v := range s.Namespaces {
		v := v
		ns = append(ns, v)
	}

	sort.Slice(ns, func(i, j int) bool {
		return ns[i].Name < ns[j].Name
	})
	return ns
}

// SortedPairs returns a sorted pair.
func (s *Service) SortedPairs() []*Pair {
	var ps []*Pair

	for _, v := range s.d.PairsMap {
		v := v
		ps = append(ps, v)
	}
	for _, v := range s.Pairs {
		v := v
		ps = append(ps, v)
	}
	sort.Slice(ps, func(i, j int) bool {
		return ps[i].Name < ps[j].Name
	})
	return ps
}

func (s *Service) SortedInfos() []*Info {
	var infos []*Info
	for _, v := range s.Infos {
		for _, v := range v {
			for _, v := range v {
				infos = append(infos, v)
			}
		}
	}

	sort.Slice(infos, func(i, j int) bool {
		return compareInfo(infos[i], infos[j])
	})
	return infos
}

func (s *Service) GetPair(name string) *Pair {
	p, ok := s.d.PairsMap[name]
	if ok {
		return p
	}

	p, ok = s.Pairs[name]
	if ok {
		return p
	}

	log.Fatalf("pair %s is not registered", name)
	return nil
}

// Namespace contains all info about a namespace
type Namespace struct {
	Features []string             `toml:"features"`
	New      *Function            `toml:"new"`
	Op       map[string]*Function `toml:"op"`

	// Runtime generated
	srv                 *Service
	Name                string
	HasFeatureLoosePair bool // Add a marker to support feature loose_pair
}

func (ns *Namespace) ParsedFeatures() []*Feature {
	var ps []*Feature

	for _, v := range ns.Features {
		f, _ := ns.srv.d.FeaturesMap[v]
		if f != nil {
			ps = append(ps, f)
		}
	}
	sort.Slice(ps, func(i, j int) bool {
		return ps[i].Name < ps[j].Name
	})
	return ps
}

func (ns *Namespace) ParsedInterface() *Interface {
	name := fmt.Sprintf("%sr", ns.Name)
	i, ok := ns.srv.d.InterfacesMap[name]
	if !ok {
		log.Fatalf("interface %s is not registered", name)
	}

	return i
}

func (ns *Namespace) ParsedFunctions() []*Function {
	var fns []*Function

	for _, v := range ns.Op {
		v := v
		fns = append(fns, v)
	}

	sort.Slice(fns, func(i, j int) bool {
		return fns[i].Name < fns[j].Name
	})
	return fns
}

type pairFunc struct {
	Pair  *Pair
	Funcs []*Function
}

func (ns *Namespace) ParsedDefaultable() []*pairFunc {
	m := make(map[*Pair][]*Function)

	for _, v := range ns.ParsedFunctions() {
		v := v
		for _, name := range v.Optional {
			p := ns.srv.GetPair(name)
			if p.Defaultable {
				m[p] = append(m[p], v)
			}
		}
	}

	var ps []*pairFunc
	for p, fn := range m {
		p, fn := p, fn
		pfn := &pairFunc{
			Pair:  p,
			Funcs: fn,
		}
		sort.Slice(pfn.Funcs, func(i, j int) bool {
			return pfn.Funcs[i].Name < pfn.Funcs[j].Name
		})
		ps = append(ps, pfn)
	}

	sort.Slice(ps, func(i, j int) bool {
		return ps[i].Pair.Name < ps[j].Pair.Name
	})
	return ps
}

// Feature is all global features that available.
//
// Feature will be defined in features.toml.
type Feature struct {
	Description string   `toml:"description"`
	Virtual     bool     `toml:"virtual"`
	Namespaces  []string `toml:"namespaces"`

	// Runtime generated.
	Name string
}

// Pair is the pair definition.
type Pair struct {
	Name        string
	Package     string `toml:"package"`
	Type        string `toml:"type"`
	Defaultable bool   `toml:"defaultable"`
	Description string `toml:"description"`

	// Runtime generated
	Global bool
}

// Info is the metadata definition.
type Info struct {
	Export      bool   `toml:"export"`
	Description string `toml:"description"`
	Package     string `toml:"package"`
	Type        string `toml:"type"`

	// Runtime generated.
	Scope    string
	Category string
	Name     string
	Global   bool
}

func (i *Info) TypeName() string {
	if i.Export {
		return templateutils.ToPascal(i.Name)
	} else {
		return templateutils.ToCamel(i.Name)
	}
}

func (i *Info) DisplayName() string {
	return templateutils.ToPascal(i.Name)
}

// Interface represents an interface
type Interface struct {
	Description string                `toml:"description"`
	Op          map[string]*Operation `toml:"op"`

	// Runtime generated
	Name string
}

func (i *Interface) SortedOps() []*Operation {
	var ops []*Operation

	for _, v := range i.Op {
		v := v
		ops = append(ops, v)
	}

	sort.Slice(ops, func(i, j int) bool {
		return ops[i].Name < ops[j].Name
	})
	return ops
}

// DisplayName will output interface's display name.
func (i *Interface) DisplayName() string {
	return templateutils.ToPascal(i.Name)
}

// Operation represents an operation.
type Operation struct {
	Description string   `toml:"description"`
	Pairs       []string `toml:"pairs"`
	Params      []string `toml:"params"`
	Results     []string `toml:"results"`
	ObjectMode  string   `toml:"object_mode"`
	Local       bool     `toml:"local"`

	// Runtime generated.
	d    *Data
	Name string
}

func (op *Operation) ParsedParams() []*Field {
	var fs []*Field
	for _, f := range op.Params {
		fs = append(fs, op.d.FieldsMap[f])
	}
	return fs
}

func (op *Operation) ParsedResults() []*Field {
	var fs []*Field
	for _, f := range op.Results {
		fs = append(fs, op.d.FieldsMap[f])
	}
	return fs
}

// Function represents a function.
type Function struct {
	Required []string `toml:"required"`
	Optional []string `toml:"optional"`

	// Runtime generated.
	srv         *Service
	ns          *Namespace
	Name        string
	Implemented bool // flag for whether this function has been implemented or not.
}

func (f *Function) ParsedRequired() []*Pair {
	var ps []*Pair
	for _, v := range f.Required {
		ps = append(ps, f.srv.GetPair(v))
	}

	sort.Slice(ps, func(i, j int) bool {
		return ps[i].Name < ps[j].Name
	})
	return ps
}

func (f *Function) ParsedOptional() []*Pair {
	var ps []*Pair
	for _, v := range f.Optional {
		ps = append(ps, f.srv.GetPair(v))
	}

	sort.Slice(ps, func(i, j int) bool {
		return ps[i].Name < ps[j].Name
	})
	return ps
}

func (f *Function) GetOperation() *Operation {
	op, ok := f.srv.d.OperationsMap[f.ns.Name][f.Name]
	if !ok {
		log.Fatalf("operation %s in namespace %s is not registered", f.Name, f.ns.Name)
	}
	return op
}

// Field represents a field.
type Field struct {
	Package string `toml:"package"`
	Type    string `toml:"type"`

	// Runtime generated.
	Name string
}

func CompleteType(dstPackage string, srcPackage string, srcType string) string {
	if srcPackage == "" || srcPackage == dstPackage {
		return srcType
	}

	index := 0
	for i, r := range srcType {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') {
			index = i
			break
		}
	}
	completeType := fmt.Sprintf("%s%s.%s", srcType[:index], srcPackage, srcType[index:])
	return completeType
}

func parseTOML(src []byte, in interface{}) (err error) {
	return toml.Unmarshal(src, in)
}

func loadBindata(filepath string) (bs []byte) {
	bs, err := definitions.Bindata.ReadFile(filepath)
	if err != nil {
		panic(fmt.Errorf("read file %s: %v", filepath, err))
	}
	return bs
}

func compareInfo(x, y *Info) bool {
	if x.Scope != y.Scope {
		return x.Scope < y.Scope
	}
	if x.Category != y.Category {
		return x.Category < y.Category
	}
	return x.Name < y.Name
}
