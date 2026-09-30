// Package environmenttemplates owns Environment Templates: saved, tenant-owned
// hosted Environment configuration that Sessions reference at creation. It owns
// the Template vocabulary, the rules for Template input and the create, update
// and delete operations. Reader reads Templates, including the decrypted
// configuration Session creation composes over.
package environmenttemplates
