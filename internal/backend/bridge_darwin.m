#import <Foundation/Foundation.h>
#import <IOBluetooth/IOBluetooth.h>
#include "bridge_darwin.h"
#include <string.h>

static NSArray<IOBluetoothDevice *> *devices;
static void refresh(void) { [devices release]; devices = [[IOBluetoothDevice pairedDevices] retain]; }
void betty_refresh(void) { @autoreleasepool { refresh(); } }
int betty_paired_count(void) { @autoreleasepool { return (int)devices.count; } }
char *betty_paired_name(int i) { @autoreleasepool { if (i < 0 || i >= devices.count) return strdup(""); return strdup([[[devices objectAtIndex:i] nameOrAddress] UTF8String]); } }
char *betty_paired_address(int i) { @autoreleasepool { if (i < 0 || i >= devices.count) return strdup(""); return strdup([[[devices objectAtIndex:i] addressString] UTF8String]); } }
int betty_paired_connected(int i) { @autoreleasepool { if (i < 0 || i >= devices.count) return 0; return [[devices objectAtIndex:i] isConnected] ? 1 : 0; } }
int betty_device_paired(int i) { @autoreleasepool { if (i < 0 || i >= devices.count) return 0; return [[devices objectAtIndex:i] isPaired] ? 1 : 0; } }
static IOBluetoothDevice *device(const char *address) { return [IOBluetoothDevice deviceWithAddressString:[NSString stringWithUTF8String:address]]; }
int betty_connect(const char *address) { @autoreleasepool { return (int)[device(address) openConnection]; } }
int betty_disconnect(const char *address) { @autoreleasepool { return (int)[device(address) closeConnection]; } }

@interface BettyPairDelegate : NSObject <IOBluetoothDevicePairDelegate>
@property BOOL finished;
@property IOReturn result;
@end
@implementation BettyPairDelegate
- (void)devicePairingFinished:(id)sender error:(IOReturn)error { self.result = error; self.finished = YES; }
@end
int betty_pair(const char *address) { @autoreleasepool {
  IOBluetoothDevicePair *pair = [IOBluetoothDevicePair pairWithDevice:device(address)];
  BettyPairDelegate *delegate = [BettyPairDelegate new]; pair.delegate = delegate;
  IOReturn started = [pair start]; if (started != kIOReturnSuccess) return (int)started;
  while (!delegate.finished) { [[NSRunLoop currentRunLoop] runUntilDate:[NSDate dateWithTimeIntervalSinceNow:0.05]]; }
  return (int)delegate.result;
} }
